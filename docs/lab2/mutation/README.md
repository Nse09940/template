# Лабораторная работа 2. Мутационное тестирование

Прогоны выполнены 6 ноября 2026 года с Gremlins v0.6.0 и Go 1.26.1 в WSL.
Область проверки — `internal/domain/...` и `internal/usecase/...`. В рабочую
копию для прогона также включён `internal/model`, от которого зависят usecase.

## Результаты до и после

| Результат | До | После |
|---|---:|---:|
| Всего сгенерировано мутаций | 43 | 43 |
| `KILLED` по Gremlins | 37 | 42 |
| Из них обнаружены прогонами тестов | 35 | 38 |
| Из них отклонены компилятором | 2 | 4 |
| `LIVED` | 2 | 0 |
| `NOT COVERED` | 4 | 1 |
| `TIMED OUT` | 0 | 0 |

Оба отчёта содержат один и тот же набор мутаций. Общий размер набора в таблице
посчитан по списку `files[].mutations`. Поле `mutants_total` в JSON Gremlins
показывает 39 и 42 соответственно, поскольку в данном прогоне оно не включает
`NOT COVERED`. [Отчёт до](before.json), [отчёт после](after.json).

## Разбор выживших и непокрытых мутаций

| Место | Мутация | Что обнаружили и как проверили |
|---|---|---|
| `trip.go:76:91` | `Latitude > 90` → `Latitude >= 90` | Запрещает допустимую широту `90`. Случай `maximum latitude` проверяет успешную валидацию. После правки тестов статус `KILLED`. |
| `trip.go:79:74` | `Longitude < -180` → `Longitude <= -180` | Запрещает допустимую долготу `-180`. Случай `minimum longitude` проверяет успешную валидацию. После правки тестов статус `KILLED`. |
| `trip.go:50:13` | `e.Field == ""` → `e.Field != ""` | Перепутаны ветки форматирования ошибки. Добавлены проверки текста с указанием поля и без него в `TestValidationErrorMessage`. Статус сменился с `NOT COVERED` на `KILLED`. |
| `trip.go:53:17`, `trip.go:53:23` | Конкатенация `+` → вычитание `-` | До добавления проверок текста Gremlins пропускал эту строку. После он помечает обе мутации `KILLED`. Отдельные прогоны с `go test -overlay` подтвердили ошибку компиляции для каждой замены. |
| `usecase.go:18:30` в `create` | `24 * time.Hour` → `24 / time.Hour` | Gremlins помечает объявление константы `NOT COVERED` в обоих прогонах. Отдельно применили эту замену через `go test -overlay`. Существующий `TestIdempotency/reserve` завершился с `invalid reservation`, поскольку проверяет срок 24 часа. |

Ещё две мутации `+` → `-` на `trip.go:77:40` и `trip.go:80:40` получили
`KILLED` в обоих прогонах. Проверка через overlay также показала ошибку
компиляции при попытке вычитать строки. Эти случаи вместе с двумя заменами на
строке 53 учтены отдельной строкой в таблице результатов. Причина расхождения
в том, что Gremlins v0.6.0 классифицирует код выхода `go test` 1 как `KILLED`.
[Код классификации Gremlins v0.6.0](https://github.com/go-gremlins/gremlins/blob/v0.6.0/internal/engine/executor.go).

Проверки границ и формата ошибки добавлены в [trip_test.go](../../../internal/domain/trip/trip_test.go).
Проверка срока ключа уже есть в [usecase_test.go](../../../internal/usecase/trip/create/usecase_test.go).
`make lint` и `make test` прошли. Покрытие операторов в `domain/trip` выросло
с 88,9% до 100%.

## Воспроизведение

Команды выполняются из корня репозитория в Linux или WSL с Go 1.26.1.
Временная копия ограничивает область запуска и изолирует мутации от рабочего
дерева. Нативный пробный запуск в Windows пометил все 43 мутации как
`NOT COVERED`. Для сравнения использованы два прогона с одинаковыми параметрами
в WSL. [Документация Gremlins](https://gremlins.dev/latest/usage/commands/unleash/).

```bash
run_dir=$(mktemp -d)
mkdir -p "$run_dir/repo/internal"
GOBIN="$run_dir/bin" go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0
cp go.mod go.sum "$run_dir/repo/"
cp -R internal/domain internal/usecase internal/model "$run_dir/repo/internal/"
(
  cd "$run_dir/repo" &&
  "$run_dir/bin/gremlins" unleash --workers 2 --timeout-coefficient 20 --output after.json
)
```

Для исходного замера используйте копию `trip_test.go` до добавления сценариев
`maximum latitude`, `minimum longitude` и `TestValidationErrorMessage`.
В этой копии запустите ту же команду с `--output before.json`. Исходники
доменной логики и usecase в обоих прогонах одинаковы.
