package cli

import (
	"encoding/json"
	"io"
)

func newJSONEncoder(w io.Writer, pretty bool) *json.Encoder {
	encoder := json.NewEncoder(w)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder
}
