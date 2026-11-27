package tests

import (
	"encoding/json"
	"fmt"
	"io"
)

// streamRootArray walks the root JSON object and calls each(dec) once per element of the array under key,
// leaving every other value skipped. Only one element is in memory at a time (R6.3).
func streamRootArray(r io.Reader, key string, each func(dec *json.Decoder) error) error {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("json report: not an object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		k, _ := kt.(string)
		if k != key {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return err
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('[') {
			return fmt.Errorf("json report: %q is not an array", key)
		}
		for dec.More() {
			if err := each(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	}
	return nil
}
