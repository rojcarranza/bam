package bamboo

import (
	"context"
	"encoding/json"
	"maps"
	"net/url"
	"strconv"
)

// envelope is Bamboo's collection wrapper:
// {"size":N,"start-index":0,"max-result":25,"<item>":[...]}.
type envelope struct {
	Size       int
	StartIndex int
	MaxResult  int
	Items      json.RawMessage
}

func (e *envelope) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] != '{' {
		return nil // top-level scalars such as "expand":"projects" are not collections
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	for k, v := range m {
		switch k {
		case "size":
			_ = json.Unmarshal(v, &e.Size)
		case "start-index":
			_ = json.Unmarshal(v, &e.StartIndex)
		case "max-result":
			_ = json.Unmarshal(v, &e.MaxResult)
		default:
			if len(v) > 0 && v[0] == '[' {
				e.Items = v
			}
		}
	}
	return nil
}

const pageSize = 100

// pageAll GETs path page by page and returns the items of the collection
// under key. limit 0 means everything.
func pageAll[T any](ctx context.Context, c *Client, path string, q url.Values, key string, limit int) ([]T, error) {
	var out []T
	start := 0
	for {
		size := pageSize
		if limit > 0 && limit-len(out) < size {
			size = limit - len(out)
		}
		qq := url.Values{}
		maps.Copy(qq, q)
		qq.Set("start-index", strconv.Itoa(start))
		qq.Set("max-result", strconv.Itoa(size))

		var root map[string]envelope
		if err := c.getJSON(ctx, path, qq, &root); err != nil {
			return nil, err
		}
		env := root[key]
		var items []T
		if len(env.Items) > 0 {
			if err := json.Unmarshal(env.Items, &items); err != nil {
				return nil, err
			}
		}
		out = append(out, items...)
		start += len(items)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil // a server may ignore max-result
		}
		if len(items) == 0 || start >= env.Size {
			return out, nil
		}
	}
}
