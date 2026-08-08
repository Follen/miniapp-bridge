package wmpf

import (
	"bytes"
	"fmt"
	"io"
	"sync/atomic"
)

type CompressAlgo uint32

const CompressNone CompressAlgo = 0
const CompressZlib CompressAlgo = 1

const (
	CategorySetupContext             = "setupContext"
	CategoryCallInterface            = "callInterface"
	CategoryCallInterfaceResult      = "callInterfaceResult"
	CategoryEvaluateJavascript       = "evaluateJavascript"
	CategoryEvaluateJavascriptResult = "evaluateJavascriptResult"
	CategoryBreakpoint               = "breakpoint"
	CategoryPing                     = "ping"
	CategoryPong                     = "pong"
	CategoryDomOp                    = "domOp"
	CategoryDomEvent                 = "domEvent"
	CategoryNetworkDebugAPI          = "networkDebugAPI"
	CategoryChromeDevtools           = "chromeDevtools"
	CategoryChromeDevtoolsResult     = "chromeDevtoolsResult"
	CategoryAddJsContext             = "addJsContext"
	CategoryRemoveJsContext          = "removeJsContext"
	CategoryConnectJsContext         = "connectJsContext"
	CategoryCustomMessage            = "customMessage"
)

type DebugMessage struct {
	Seq, After   uint32
	Category     string
	Data         []byte
	CompressAlgo CompressAlgo
	OriginalSize uint32
}
type ChromeDevtools struct {
	OpID                 uint64
	Payload, JSContextID string
}
type CallInterface struct {
	ObjName, MethodName string
	MethodArgs          []string
	CallID              uint32
}
type CallInterfaceResult struct {
	Ret       string
	CallID    uint32
	DebugInfo string
}
type EvaluateJavascript struct {
	Script     string
	EvaluateID uint32
	DebugInfo  string
}
type EvaluateJavascriptResult struct {
	Ret        string
	EvaluateID uint32
}
type Ping struct {
	PingID  uint64
	Payload string
}
type Pong struct {
	PingID      uint64
	NetworkType uint32
	Payload     string
}
type Breakpoint struct{ IsHit bool }
type JsContext struct{ ID, Name string }
type CustomMessage struct {
	Method, Payload string
	Raw             []byte
}
type Unwrapped struct {
	Seq, Delay   uint32
	Category     string
	Data         any
	CompressAlgo CompressAlgo
	OriginalSize uint32
	Raw          []byte
}

var compressionSavedBytes atomic.Int64

func ResetCompressionStatistics()  { compressionSavedBytes.Store(0) }
func CompressionSavedBytes() int64 { return compressionSavedBytes.Load() }

func putVar(b *bytes.Buffer, n uint64) {
	for n >= 128 {
		b.WriteByte(byte(n) | 128)
		n >>= 7
	}
	b.WriteByte(byte(n))
}
func putU(b *bytes.Buffer, f int, n uint64) { putVar(b, uint64(f<<3)); putVar(b, n) }
func putS(b *bytes.Buffer, f int, s string) {
	putVar(b, uint64(f<<3|2))
	putVar(b, uint64(len(s)))
	b.WriteString(s)
}
func putB(b *bytes.Buffer, f int, d []byte) {
	putVar(b, uint64(f<<3|2))
	putVar(b, uint64(len(d)))
	b.Write(d)
}
func readVar(d []byte, i *int) (uint64, error) {
	var n uint64
	for s := uint(0); ; s += 7 {
		if *i >= len(d) || s > 63 {
			return 0, io.ErrUnexpectedEOF
		}
		c := d[*i]
		*i++
		n |= uint64(c&127) << s
		if c < 128 {
			return n, nil
		}
	}
}
func readField(d []byte, i *int) (int, int, error) {
	v, e := readVar(d, i)
	return int(v >> 3), int(v & 7), e
}
func readBytes(d []byte, i *int) ([]byte, error) {
	n, e := readVar(d, i)
	if e != nil || n > uint64(len(d)-*i) {
		return nil, io.ErrUnexpectedEOF
	}
	x := d[*i : *i+int(n)]
	*i += int(n)
	return x, nil
}
func skip(d []byte, i *int, w int) error {
	switch w {
	case 0:
		_, e := readVar(d, i)
		return e
	case 1:
		if len(d)-*i < 8 {
			return io.ErrUnexpectedEOF
		}
		*i += 8
		return nil
	case 2:
		x, e := readBytes(d, i)
		_ = x
		return e
	case 3:
		for {
			if *i >= len(d) {
				return io.ErrUnexpectedEOF
			}
			_, nestedWire, err := readField(d, i)
			if err != nil {
				return err
			}
			if nestedWire == 4 {
				return nil
			}
			if err := skip(d, i, nestedWire); err != nil {
				return err
			}
		}
	case 4:
		return nil
	case 5:
		if len(d)-*i < 4 {
			return io.ErrUnexpectedEOF
		}
		*i += 4
		return nil
	default:
		return fmt.Errorf("unsupported wire type %d", w)
	}
}

func EncodeDebugMessage(m DebugMessage) []byte {
	var b bytes.Buffer
	putU(&b, 1, uint64(m.Seq))
	putU(&b, 2, uint64(m.After))
	putS(&b, 3, m.Category)
	putB(&b, 4, m.Data)
	putU(&b, 5, uint64(m.CompressAlgo))
	putU(&b, 6, uint64(m.OriginalSize))
	return b.Bytes()
}

// EncodeOutgoingDebugMessage mirrors src/index.ts outData. The reference omits
// after entirely but explicitly supplies both zero-valued compression fields.
func EncodeOutgoingDebugMessage(m DebugMessage) []byte {
	var b bytes.Buffer
	putU(&b, 1, uint64(m.Seq))
	putS(&b, 3, m.Category)
	putB(&b, 4, m.Data)
	putU(&b, 5, uint64(m.CompressAlgo))
	putU(&b, 6, uint64(m.OriginalSize))
	return b.Bytes()
}
func DecodeDebugMessage(d []byte) (DebugMessage, error) {
	var m DebugMessage
	i := 0
	for i < len(d) {
		f, w, e := readField(d, &i)
		if e != nil {
			return m, e
		}
		switch f {
		case 1:
			v, e := readVar(d, &i)
			m.Seq = uint32(v)
			if e != nil {
				return m, e
			}
		case 2:
			v, e := readVar(d, &i)
			m.After = uint32(v)
			if e != nil {
				return m, e
			}
		case 3:
			x, e := readBytes(d, &i)
			m.Category = string(x)
			if e != nil {
				return m, e
			}
		case 4:
			x, e := readBytes(d, &i)
			m.Data = append([]byte(nil), x...)
			if e != nil {
				return m, e
			}
		case 5:
			v, e := readVar(d, &i)
			m.CompressAlgo = CompressAlgo(v)
			if e != nil {
				return m, e
			}
		case 6:
			v, e := readVar(d, &i)
			m.OriginalSize = uint32(v)
			if e != nil {
				return m, e
			}
		default:
			if e := skip(d, &i, w); e != nil {
				return m, e
			}
		}
	}
	return m, nil
}

func WrapData(data []byte, category string, algo CompressAlgo) ([]byte, uint32, error) {
	if algo&CompressZlib != 0 {
		c, e := zlibCompress(data)
		if e == nil {
			compressionSavedBytes.Add(int64(len(data) - len(c)))
		}
		return c, uint32(len(data)), e
	}
	return append([]byte(nil), data...), 0, nil
}
func UnwrapDebugMessage(m DebugMessage) (Unwrapped, error) {
	raw := append([]byte(nil), m.Data...)
	d := raw
	var e error
	if m.CompressAlgo&CompressZlib != 0 {
		d, e = zlibDecompress(raw)
		if e != nil {
			return Unwrapped{Category: m.Category, Raw: raw}, e
		}
		compressionSavedBytes.Add(int64(len(d) - len(raw)))
	}
	category := m.Category
	if category == "" {
		category = CategoryPing
		d = nil
	}
	originalSize := m.OriginalSize
	if originalSize == 0 && m.CompressAlgo&CompressZlib != 0 {
		originalSize = uint32(len(raw))
	}
	u := Unwrapped{Seq: m.Seq, Delay: m.After, Category: category, CompressAlgo: m.CompressAlgo, OriginalSize: originalSize, Raw: raw}
	u.Data = d
	return u, nil
}

func EncodeChrome(m ChromeDevtools) []byte {
	var b bytes.Buffer
	if m.OpID != 0 {
		putU(&b, 1, m.OpID)
	}
	putS(&b, 2, m.Payload)
	if m.JSContextID != "" {
		putS(&b, 3, m.JSContextID)
	}
	return b.Bytes()
}
func DecodeChrome(d []byte) (ChromeDevtools, error) {
	var m ChromeDevtools
	i := 0
	for i < len(d) {
		f, w, e := readField(d, &i)
		if e != nil {
			return m, e
		}
		switch f {
		case 1:
			v, e := readVar(d, &i)
			m.OpID = v
			if e != nil {
				return m, e
			}
		case 2:
			x, e := readBytes(d, &i)
			m.Payload = string(x)
			if e != nil {
				return m, e
			}
		case 3:
			x, e := readBytes(d, &i)
			m.JSContextID = string(x)
			if e != nil {
				return m, e
			}
		default:
			if e := skip(d, &i, w); e != nil {
				return m, e
			}
		}
	}
	return m, nil
}

func EncodeCustom(m CustomMessage) []byte {
	var b bytes.Buffer
	putS(&b, 1, m.Method)
	putS(&b, 2, m.Payload)
	if m.Raw != nil {
		putB(&b, 3, m.Raw)
	}
	return b.Bytes()
}
func DecodeCustom(d []byte) (CustomMessage, error) {
	var m CustomMessage
	i := 0
	for i < len(d) {
		f, w, e := readField(d, &i)
		if e != nil {
			return m, e
		}
		switch f {
		case 1:
			x, e := readBytes(d, &i)
			m.Method = string(x)
			if e != nil {
				return m, e
			}
		case 2:
			x, e := readBytes(d, &i)
			m.Payload = string(x)
			if e != nil {
				return m, e
			}
		case 3:
			x, e := readBytes(d, &i)
			m.Raw = append([]byte(nil), x...)
			if e != nil {
				return m, e
			}
		default:
			if e := skip(d, &i, w); e != nil {
				return m, e
			}
		}
	}
	return m, nil
}

func DecodeCategory(category string, d []byte) (any, error) {
	decode := func(out any) (any, error) {
		if err := UnmarshalMessage(d, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	switch category {
	case "", CategoryPing:
		if d == nil {
			return map[string]any{}, nil
		}
		var p PingProto
		if _, err := decode(&p); err != nil {
			return nil, err
		}
		return Ping{PingID: p.PingId, Payload: p.Payload}, nil
	case CategorySetupContext:
		var v SetupContext
		return decode(&v)
	case CategoryCallInterface:
		var v CallInterfaceProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return CallInterface{ObjName: v.ObjName, MethodName: v.MethodName, MethodArgs: v.MethodArgs, CallID: v.CallId}, nil
	case CategoryCallInterfaceResult:
		var v CallInterfaceResultProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return CallInterfaceResult{Ret: v.Ret, CallID: v.CallId, DebugInfo: v.DebugInfo}, nil
	case CategoryEvaluateJavascript:
		var v EvaluateJavascriptProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return EvaluateJavascript{Script: v.Script, EvaluateID: v.EvaluateId, DebugInfo: v.DebugInfo}, nil
	case CategoryEvaluateJavascriptResult:
		var v EvaluateJavascriptResultProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return EvaluateJavascriptResult{Ret: v.Ret, EvaluateID: v.EvaluateId}, nil
	case CategoryBreakpoint:
		var v BreakpointProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return Breakpoint{IsHit: v.IsHit}, nil
	case CategoryPong:
		var v PongProto
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return Pong{PingID: v.PingId, NetworkType: v.NetworkType, Payload: v.Payload}, nil
	case CategoryDomOp:
		var v DomOp
		return decode(&v)
	case CategoryDomEvent:
		var v DomEvent
		return decode(&v)
	case CategoryNetworkDebugAPI:
		var v NetworkDebugAPI
		return decode(&v)
	case CategoryChromeDevtools, CategoryChromeDevtoolsResult:
		return DecodeChrome(d)
	case CategoryAddJsContext:
		var v AddJsContext
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return JsContext{ID: v.JscontextId, Name: v.JscontextName}, nil
	case CategoryRemoveJsContext:
		var v RemoveJsContext
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return JsContext{ID: v.JscontextId}, nil
	case CategoryConnectJsContext:
		var v ConnectJsContext
		if _, err := decode(&v); err != nil {
			return nil, err
		}
		return JsContext{ID: v.JscontextId}, nil
	case CategoryCustomMessage:
		return DecodeCustom(d)
	default:
		return map[string]any{}, nil
	}
}

func EncodeCategory(category string, value any) ([]byte, error) {
	switch category {
	case "", CategoryPing:
		v, ok := value.(Ping)
		if !ok {
			return nil, fmt.Errorf("%s expects Ping", category)
		}
		var b bytes.Buffer
		putU(&b, 1, v.PingID)
		putS(&b, 2, v.Payload)
		return b.Bytes(), nil
	case CategorySetupContext:
		return MarshalMessage(value)
	case CategoryCallInterface:
		v, ok := value.(CallInterface)
		if !ok {
			return nil, fmt.Errorf("%s expects CallInterface", category)
		}
		var b bytes.Buffer
		putS(&b, 1, v.ObjName)
		putS(&b, 2, v.MethodName)
		for _, arg := range v.MethodArgs {
			putS(&b, 3, arg)
		}
		putU(&b, 4, uint64(v.CallID))
		return b.Bytes(), nil
	case CategoryCallInterfaceResult:
		v, ok := value.(CallInterfaceResult)
		if !ok {
			return nil, fmt.Errorf("%s expects CallInterfaceResult", category)
		}
		return MarshalMessage(CallInterfaceResultProto{Ret: v.Ret, CallId: v.CallID, DebugInfo: v.DebugInfo})
	case CategoryEvaluateJavascript:
		v, ok := value.(EvaluateJavascript)
		if !ok {
			return nil, fmt.Errorf("%s expects EvaluateJavascript", category)
		}
		return MarshalMessage(EvaluateJavascriptProto{Script: v.Script, EvaluateId: v.EvaluateID, DebugInfo: v.DebugInfo})
	case CategoryEvaluateJavascriptResult:
		v, ok := value.(EvaluateJavascriptResult)
		if !ok {
			return nil, fmt.Errorf("%s expects EvaluateJavascriptResult", category)
		}
		var b bytes.Buffer
		putS(&b, 1, v.Ret)
		putU(&b, 2, uint64(v.EvaluateID))
		return b.Bytes(), nil
	case CategoryBreakpoint:
		v, ok := value.(Breakpoint)
		if !ok {
			return nil, fmt.Errorf("%s expects Breakpoint", category)
		}
		var b bytes.Buffer
		if v.IsHit {
			putU(&b, 1, 1)
		} else {
			putU(&b, 1, 0)
		}
		return b.Bytes(), nil
	case CategoryPong:
		v, ok := value.(Pong)
		if !ok {
			return nil, fmt.Errorf("%s expects Pong", category)
		}
		return MarshalMessage(PongProto{PingId: v.PingID, NetworkType: v.NetworkType, Payload: v.Payload})
	case CategoryDomOp, CategoryDomEvent, CategoryNetworkDebugAPI:
		return MarshalMessage(value)
	case CategoryChromeDevtools, CategoryChromeDevtoolsResult:
		v, ok := value.(ChromeDevtools)
		if !ok {
			return nil, fmt.Errorf("%s expects ChromeDevtools", category)
		}
		var b bytes.Buffer
		putU(&b, 1, v.OpID)
		putS(&b, 2, v.Payload)
		putS(&b, 3, v.JSContextID)
		return b.Bytes(), nil
	case CategoryAddJsContext:
		v, ok := value.(JsContext)
		if !ok {
			return nil, fmt.Errorf("%s expects JsContext", category)
		}
		return MarshalMessage(AddJsContext{JscontextId: v.ID, JscontextName: v.Name})
	case CategoryRemoveJsContext:
		v, ok := value.(JsContext)
		if !ok {
			return nil, fmt.Errorf("%s expects JsContext", category)
		}
		return MarshalMessage(RemoveJsContext{JscontextId: v.ID})
	case CategoryConnectJsContext:
		v, ok := value.(JsContext)
		if !ok {
			return nil, fmt.Errorf("%s expects JsContext", category)
		}
		return MarshalMessage(ConnectJsContext{JscontextId: v.ID})
	case CategoryCustomMessage:
		v, ok := value.(CustomMessage)
		if !ok {
			return nil, fmt.Errorf("%s expects CustomMessage", category)
		}
		return EncodeCustom(v), nil
	default:
		v, ok := value.([]byte)
		if !ok {
			return nil, fmt.Errorf("unknown category %q expects raw bytes", category)
		}
		return append([]byte(nil), v...), nil
	}
}
