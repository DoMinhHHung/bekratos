package httpjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const MaxBodyBytes = 1 << 20

func Decode(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, MaxBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > MaxBodyBytes {
		return errors.New("body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
