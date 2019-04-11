package h2quicsim

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/net/http2/hpack"
)

// ErrDB is returned when your db format is wrong
var ErrDB = errors.New("entry db format error")

// KVP is a key-value pair for headers
type KVP = hpack.HeaderField

// Entry is an web object
type Entry struct {
	URL        string `json:"url"`
	Connection int
	Initiator  string
	Size       int

	Stream     int
	Dependency int
	Weight     int

	Request  []KVP
	Response []KVP
}

func encodeObject(headers []KVP) string {
	var path, authority, method string
	for _, header := range headers {
		switch header.Name {
		case ":path":
			path = header.Value
		case ":authority":
			authority = header.Value
		case ":method":
			method = header.Value
		}
	}
	return fmt.Sprintf("%s %s%s", method, authority, path)
}

func msec(st, cu time.Time) int {
	return int(cu.Sub(st) / time.Millisecond)
}

// LoadObjects read from file
func LoadObjects(r io.Reader) ([]Entry, error) {
	var ret []Entry
	dec := json.NewDecoder(r)
	err := dec.Decode(&ret)
	if err != nil {
		return nil, err
	}
	return ret, nil
}
