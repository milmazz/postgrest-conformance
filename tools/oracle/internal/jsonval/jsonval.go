package jsonval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

func DecodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}

func numRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(n.String())
		return r, ok
	case int:
		return new(big.Rat).SetInt64(int64(n)), true
	case int64:
		return new(big.Rat).SetInt64(n), true
	case uint64:
		return new(big.Rat).SetUint64(n), true
	case float64:
		r, ok := new(big.Rat).SetString(fmt.Sprintf("%v", n))
		return r, ok
	}
	return nil, false
}

func DeepEqual(a, b any) bool {
	if ra, ok := numRat(a); ok {
		rb, ok2 := numRat(b)
		return ok2 && ra.Cmp(rb) == 0
	}
	switch av := a.(type) {
	case nil:
		return b == nil
	case string:
		bs, ok := b.(string)
		return ok && av == bs
	case bool:
		bb, ok := b.(bool)
		return ok && av == bb
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bvv, ok := bv[k]
			if !ok || !DeepEqual(v, bvv) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !DeepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return false
}
