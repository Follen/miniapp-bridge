package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const HelpText = `Usage: miniapp-bridge [options]

Options:
  --debug-port <port>  Remote debug server port (default: 9421)
  --cdp-port <port>    CDP proxy server port (default: 62000)
  --debug-main         Output main process debug messages
  --debug-frida        Output Frida client messages
  --record <file>      Record incoming raw debug frames
  --replay <file>      Replay recorded raw debug frames
  -h, --help           Show this help message`

const (
	DefaultDebugPort = 9421
	DefaultCDPPort   = 62000
)

type Options struct {
	DebugPort, CDPPort     int
	DebugMain, DebugFrida  bool
	RecordPath, ReplayPath string
}

func Parse(args []string) (Options, error) {
	o := Options{DebugPort: DefaultDebugPort, CDPPort: DefaultCDPPort}
	var debugPort, cdpPort *string
	help := false
	for i := 0; i < len(args); i++ {
		name, inlineValue, hasInlineValue := splitOption(args[i])
		switch name {
		case "-h", "--help":
			if hasInlineValue {
				return o, fmt.Errorf("[main] unknown option: %s", args[i])
			}
			help = true
		case "--debug-main":
			if hasInlineValue {
				return o, fmt.Errorf("[main] unknown option: %s", args[i])
			}
			o.DebugMain = true
		case "--debug-frida":
			if hasInlineValue {
				return o, fmt.Errorf("[main] unknown option: %s", args[i])
			}
			o.DebugFrida = true
		case "--record", "--replay":
			value := inlineValue
			if !hasInlineValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return o, fmt.Errorf("[main] missing value for %s", name)
				}
				i++
				value = args[i]
			}
			if name == "--record" {
				o.RecordPath = value
			} else {
				o.ReplayPath = value
			}
		case "--debug-port", "--cdp-port":
			value := inlineValue
			if !hasInlineValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return o, fmt.Errorf("[main] missing value for %s", name)
				}
				i++
				value = args[i]
			}
			if name == "--debug-port" {
				debugPort = &value
			} else {
				cdpPort = &value
			}
		default:
			return o, fmt.Errorf("[main] unknown option: %s", args[i])
		}
	}
	if help {
		return o, ErrHelp
	}
	for _, item := range []struct {
		name  string
		value *string
		set   func(int)
	}{
		{"--debug-port", debugPort, func(port int) { o.DebugPort = port }},
		{"--cdp-port", cdpPort, func(port int) { o.CDPPort = port }},
	} {
		if item.value == nil {
			continue
		}
		n, err := parsePort(*item.value)
		if err != nil {
			return o, fmt.Errorf("[main] invalid %s: %s", item.name, *item.value)
		}
		item.set(n)
	}
	return o, nil
}

func splitOption(arg string) (name, value string, hasValue bool) {
	if strings.HasPrefix(arg, "--") {
		if index := strings.IndexByte(arg, '='); index >= 0 {
			return arg[:index], arg[index+1:], true
		}
	}
	return arg, "", false
}

func parsePort(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, "_") {
		return 0, fmt.Errorf("empty port")
	}
	var number float64
	var err error
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "0b") || strings.HasPrefix(lower, "0o") {
		integer, parseErr := strconv.ParseUint(lower[2:], map[byte]int{'x': 16, 'b': 2, 'o': 8}[lower[1]], 64)
		number, err = float64(integer), parseErr
	} else {
		number, err = strconv.ParseFloat(trimmed, 64)
	}
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 1 || number > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return int(number), nil
}

var ErrHelp = fmt.Errorf("help")

type AddressConfig struct {
	Version             int    `json:"Version"`
	LoadStartHookOffset string `json:"LoadStartHookOffset"`
	CDPFilterHookOffset string `json:"CDPFilterHookOffset"`
	SceneOffsets        []int  `json:"SceneOffsets"`
}

func LoadAddress(dir string, version int) (AddressConfig, error) {
	var c AddressConfig
	b, e := os.ReadFile(fmt.Sprintf("%s/addresses.%d.json", dir, version))
	if e != nil {
		return c, fmt.Errorf("[frida] version config not found: %d", version)
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	return c, nil
}
