package model

import (
	"fmt"
	"strings"
)

type Hub struct {
	Hasher string `json:"hasher"`
	URL    string `json:"url"`
}

type ValidationError struct {
	Fields map[string]interface{}
}

func (v ValidationError) Error() string {
	ret := make([]string, 0)

	for key, val := range v.Fields {
		ret = append(ret, fmt.Sprintf("%s=%v", key, val))
	}

	return strings.Join(ret, ", ")
}
