# TripGo — лабораторная работа №1

TripGo — HTTP-сервис поездок из курса «Разработка микросервисов на Go».
В первой лабораторной реализованы создание, получение и завершение поездки,
PostgreSQL-репозитории, миграции и контекстный менеджер транзакций.

## Требования

- Go 1.26 или новее;
- утилита `tripgoctl` из
  [course-infra](https://github.com/course-go-autumn-2026/course-infra);
- GNU Make;
- Docker — для сборки и запуска контейнера.

## Быстрый старт

```bash
tripgoctl cluster start
tripgoctl environment start
tripgoctl connect

make migrate
make run
```

`tripgoctl environment start` читает корневой
[`environment.toml`](environment.toml) и создаёт локальный `.env`. Makefile
сначала загружает безопасные значения из `.env.example`, затем адреса окружения
из `.env`. Сервис по умолчанию доступен на `http://localhost:8080`.

Проверка:

```bash
curl -sS -X POST http://localhost:8080/api/v1/trips \
  -H 'Content-Type: application/json' \
  -d '{
    "user_id": "5cb72c04-7650-45c9-a79b-bcdba0631e0c",
    "driver_id": "8860b315-ec86-42eb-a17c-7c163d721ff5",
    "start_point": {"latitude": 59.9398, "longitude": 30.3146},
    "end_point": {"latitude": 59.9290, "longitude": 30.3626},
    "price": 1450
  }'

curl -sS http://localhost:8080/health
curl -sS http://localhost:8080/ready
```

## Команды

| Команда | Назначение |
|---|---|
| `make generate` | сгенерировать модели и chi-интерфейсы из OpenAPI |
| `make migrate` | применить все миграции |
| `make migrate-down` | откатить последнюю миграцию |
| `make run` | запустить сервис |
| `make build` | проверить сборку всех пакетов |
| `make docker-build` | собрать образ `trip-service:lab1` |
| `make test` | запустить тесты с race detector |
| `make lint` | запустить `go vet` |

## Переменные окружения

Полный пример находится в [`.env.example`](.env.example).

| Переменная | Назначение |
|---|---|
| `HTTP_ADDR` | адрес HTTP-сервера |
| `HTTP_READ_TIMEOUT` | максимальное время чтения запроса |
| `HTTP_READ_HEADER_TIMEOUT` | максимальное время чтения заголовков |
| `HTTP_WRITE_TIMEOUT` | максимальное время записи ответа |
| `HTTP_IDLE_TIMEOUT` | таймаут keep-alive соединения |
| `LOG_LEVEL` | уровень JSON-логов: debug, info, warn или error |
| `SHUTDOWN_TIMEOUT` | общий бюджет graceful shutdown |
| `DATABASE_URL` | DSN PostgreSQL |
| `DATABASE_MAX_CONNS` | максимальный размер пула |
| `DATABASE_MIN_CONNS` | минимальный размер пула |
| `DATABASE_MAX_CONN_LIFETIME` | максимальное время жизни соединения |
| `DATABASE_CONNECT_TIMEOUT` | таймаут подключения и стартового Ping |
| `DATABASE_QUERY_TIMEOUT` | таймаут запроса и транзакции |

Все переменные обязательны. При пропущенном или некорректном значении сервис
завершается на старте с понятной ошибкой.

## HTTP API

| Метод | Путь | Назначение |
|---|---|---|
| `POST` | `/api/v1/trips` | создать поездку |
| `GET` | `/api/v1/trips/{tripId}` | получить поездку |
| `POST` | `/api/v1/trips/{tripId}/finish` | завершить поездку |
| `GET` | `/health` | проверить, что процесс жив |
| `GET` | `/ready` | проверить доступность PostgreSQL |

Успешные ответы имеют `Content-Type: application/json`. Ошибки возвращаются в
формате RFC 9457 с `Content-Type: application/problem+json` и стабильным полем
`code`.

## Принятые решения

### Запрет двух активных поездок

Инвариант закреплён частичным уникальным индексом:

```sql
CREATE UNIQUE INDEX trips_one_active_per_driver_idx
    ON trips (driver_id)
    WHERE status = 'active';
```

Поэтому два конкурентных `POST /api/v1/trips` для одного водителя не могут
создать две активные поездки. Ошибка PostgreSQL `23505` по этому индексу
преобразуется в доменную ошибку `driver_busy` и HTTP `409`.

### Идемпотентное создание поездки

Клиент может передать UUID в заголовке `Idempotency-Key`. Ключ действует 24
часа. Сервис хранит в PostgreSQL SHA-256 нормализованного тела запроса, `trip_id`
и время истечения ключа.

Первый запрос резервирует ключ и создаёт поездку в одной транзакции. Повтор с
тем же ключом и телом возвращает ту же поездку с HTTP `200`; с другим телом —
`409 idempotency_conflict`. Без заголовка создание работает как обычно и
возвращает `201`.

Резервирование выполняется до вставки поездки. Внешний ключ на заранее
сгенерированный `trip_id` объявлен `DEFERRABLE INITIALLY DEFERRED`, поэтому он
проверяется при commit. Параллельные запросы с одним ключом блокируются на его
уникальном индексе: один создаёт поездку, остальные после commit читают её и не
создают дубликаты. Истёкшая запись удаляется при следующем использовании ключа.

### Менеджер транзакций

`TxManager.Do` открывает транзакцию, помещает её в `context.Context` и
вызывает бизнес-функцию с новым контекстом. Репозиторий сам выбирает исполнитель:
транзакцию из контекста либо обычный пул. Поэтому бизнес-код не импортирует
`pgx` и не передаёт транзакции аргументами.

При `nil` выполняется commit, при ошибке или panic — rollback. Вложенный
`Do` переиспользует текущую транзакцию. Создание поездки и начальная запись
`trip_status_history` выполняются атомарно.

Использован уровень изоляции `READ COMMITTED`: он достаточен, поскольку
конкурентный запрет активных поездок обеспечивает уникальный индекс, а
завершение защищено условным атомарным
`UPDATE ... WHERE status = 'active'`. Более строгая изоляция добавила бы
конфликты и повторы без дополнительной гарантии для этих операций.

### Конкурентное завершение

Завершение меняет только строку со статусом `active` и возвращает её через
`RETURNING`. При двух одновременных запросах обновление выполнит только один.
Второй запрос увидит уже завершённую поездку и получит `409 trip_completed`;
`finished_at` не перезаписывается.

### Health и readiness

`/health` не обращается к базе и сообщает только о жизни процесса. `/ready`
выполняет `Ping` PostgreSQL с таймаутом и возвращает `503`, если сервис пока
не способен обслуживать запросы.

## Docker

Образ собирается в два этапа. В builder-слое компилируется статический Go-бинарь
с `CGO_ENABLED=0`; в итоговый слой `distroless/static-debian12:nonroot` копируется
только бинарь. Процесс запускается как `nonroot:nonroot`. В образе нет исходного
кода, Go toolchain, кеша сборки и shell.

`.dockerignore` исключает `.git`, тесты, `.env`, `.tripgo`, настройки IDE и
локальные материалы. Размер итогового образа для `linux/amd64`, измеренный через
`docker image inspect`, — **4.54 MiB**.

Сборка:

```bash
make docker-build
```

PostgreSQL сначала запускается через `tripgoctl`, миграции применяются с хоста:

```bash
tripgoctl environment start
make migrate
```

При запуске контейнера адрес `localhost` из `.env` заменяется на специальное имя
хоста Docker Desktop:

```bash
DATABASE_URL="$(sed -n 's/^DATABASE_URL=//p' .env)"

docker run --rm -p 8080:8080 \
  --env-file .env.example \
  --env-file .env \
  --env "DATABASE_URL=${DATABASE_URL/localhost/host.docker.internal}" \
  trip-service:lab1
```

Проверка контейнера:

```bash
curl -i http://localhost:8080/ready
```
