package create

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/Nse09940/template/internal/generated"
)

const maxRequestBodyBytes = 1 << 20

func requireJSONContentType(r *http.Request) error {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return errors.New("Content-Type must be application/json")
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}

	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		//nolint:staticcheck // Preserve the invalid-request detail returned by the first lab.
		return errors.New("Request body must contain exactly one JSON object")
	}
	return nil
}

func decodeTripData(w http.ResponseWriter, r *http.Request) (api.TripData, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return api.TripData{}, err
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		return api.TripData{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return api.TripData{}, errors.New("request body must be a JSON object")
	}
	if err := requireFields(fields, "user_id", "driver_id", "start_point", "end_point", "price"); err != nil {
		return api.TripData{}, err
	}
	if err := requireCoordinateFields(fields["start_point"], "start_point"); err != nil {
		return api.TripData{}, err
	}
	if err := requireCoordinateFields(fields["end_point"], "end_point"); err != nil {
		return api.TripData{}, err
	}

	var body api.TripData
	typedDecoder := json.NewDecoder(bytes.NewReader(raw))
	typedDecoder.DisallowUnknownFields()
	if err := typedDecoder.Decode(&body); err != nil {
		return api.TripData{}, err
	}

	return body, nil
}

func requireCoordinateFields(raw json.RawMessage, name string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("%s must be a JSON object", name)
	}
	return requireFields(fields, "latitude", "longitude")
}

func requireFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			return fmt.Errorf("required field %q is missing", name)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %q must not be null", name)
		}
	}
	return nil
}
