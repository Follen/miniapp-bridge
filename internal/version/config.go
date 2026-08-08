package version

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type AddressConfig struct {
	Version             int    `json:"Version"`
	LoadStartHookOffset string `json:"LoadStartHookOffset"`
	CDPFilterHookOffset string `json:"CDPFilterHookOffset"`
	SceneOffsets        []int  `json:"SceneOffsets"`
}

func (c AddressConfig) Offset(value string) (uint64, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, errors.New("empty offset")
	}
	if strings.HasPrefix(strings.ToLower(v), "0x") {
		return strconv.ParseUint(v[2:], 16, 64)
	}
	return strconv.ParseUint(v, 10, 64)
}

func LoadFile(path string) (AddressConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return AddressConfig{}, err
	}
	var c AddressConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("decode addresses: %w", err)
	}
	if c.Version == 0 || len(c.SceneOffsets) == 0 {
		return c, errors.New("invalid address config")
	}
	return c, nil
}

func LoadDir(dir string) (map[int]AddressConfig, error) {
	result := make(map[int]AddressConfig)
	files, err := filepath.Glob(filepath.Join(dir, "addresses.*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		c, e := LoadFile(f)
		if e != nil {
			return nil, e
		}
		result[c.Version] = c
	}
	if len(result) == 0 {
		return result, errors.New("no address configs found")
	}
	return result, nil
}

func Select(configs map[int]AddressConfig, version int) (AddressConfig, error) {
	if c, ok := configs[version]; ok {
		return c, nil
	}
	return AddressConfig{}, fmt.Errorf("unsupported version %d", version)
}
