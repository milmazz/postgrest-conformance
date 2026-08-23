package jsonval

import (
	"fmt"
	"strconv"
	"strings"
)

type pathStep struct {
	key     string
	idx     int
	isIndex bool
}

func parsePath(p string) ([]pathStep, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("jsonpath %q: must start with $", p)
	}
	rest := p[1:]
	var steps []pathStep
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			if strings.HasPrefix(rest, "..") {
				return nil, fmt.Errorf("jsonpath %q: recursive descent unsupported", p)
			}
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end == -1 {
				end = len(rest)
			}
			if end == 0 {
				return nil, fmt.Errorf("jsonpath %q: empty member name", p)
			}
			steps = append(steps, pathStep{key: rest[:end]})
			rest = rest[end:]
		case '[':
			end := strings.IndexByte(rest, ']')
			if end == -1 {
				return nil, fmt.Errorf("jsonpath %q: unclosed bracket", p)
			}
			inner := rest[1:end]
			if strings.HasPrefix(inner, "'") && strings.HasSuffix(inner, "'") && len(inner) >= 2 {
				key := inner[1 : len(inner)-1]
				if strings.ContainsAny(key, `'\`) {
					return nil, fmt.Errorf("jsonpath %q: quoted-key escapes unsupported", p)
				}
				steps = append(steps, pathStep{key: key})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("jsonpath %q: unsupported selector [%s]", p, inner)
				}
				steps = append(steps, pathStep{idx: n, isIndex: true})
			}
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("jsonpath %q: unexpected %q", p, rest[0])
		}
	}
	return steps, nil
}

func EvalPath(docv any, path string) (any, bool, error) {
	steps, err := parsePath(path)
	if err != nil {
		return nil, false, err
	}
	cur := docv
	for _, s := range steps {
		if s.isIndex {
			arr, ok := cur.([]any)
			if !ok || s.idx >= len(arr) {
				return nil, false, nil
			}
			cur = arr[s.idx]
		} else {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false, nil
			}
			v, ok := m[s.key]
			if !ok {
				return nil, false, nil
			}
			cur = v
		}
	}
	return cur, true, nil
}
