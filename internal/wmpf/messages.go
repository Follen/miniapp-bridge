package wmpf

// Runtime representations of the WMPF protobuf messages. The codec below is
// deliberately reflection based so every message uses the same wire rules and
// unknown fields can be preserved without generated runtime dependencies.
import (
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"strings"
)

type ProtoUnknown struct {
	UnknownFields []byte `json:"-"`
}

type BaseReq struct {
	ProtoUnknown
	ClientVersion uint32 `pb:"1,varint"`
}
type BaseResp struct {
	ProtoUnknown
	Errcode int32  `pb:"1,varint"`
	Errmsg  string `pb:"2,string"`
}
type DataFormat struct {
	ProtoUnknown
	Cmd  uint32 `pb:"1,varint"`
	Uuid string `pb:"2,string"`
	Data []byte `pb:"3,bytes"`
}
type CommReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
}
type CommResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type DebugMessageProto struct {
	ProtoUnknown
	Seq          uint32 `pb:"1,varint"`
	After        uint32 `pb:"2,varint"`
	Category     string `pb:"3,string"`
	Data         []byte `pb:"4,bytes"`
	CompressAlgo uint32 `pb:"5,varint"`
	OriginalSize uint32 `pb:"6,varint"`
}
type SendDebugMessageReq struct {
	ProtoUnknown
	BaseRequest      *BaseReq            `pb:"1,msg"`
	DebugMessageList []DebugMessageProto `pb:"2,msg"`
	RecvAck          uint32              `pb:"3,varint"`
}
type SendDebugMessageResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
	SendAck      uint32    `pb:"2,varint"`
}
type NewSendDebugMessageReq struct {
	ProtoUnknown
	BaseRequest      *BaseReq            `pb:"1,msg"`
	DebugMessageList []DebugMessageProto `pb:"2,msg"`
	RecvAck          uint32              `pb:"3,varint"`
}
type NewSendDebugMessageResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
	MinAck       uint32    `pb:"2,varint"`
	MaxAck       uint32    `pb:"3,varint"`
}
type MessageNotify struct {
	ProtoUnknown
	DebugMessageList []DebugMessageProto `pb:"1,msg"`
}
type MessageNotifyResp struct {
	ProtoUnknown
	RecvAck uint32 `pb:"1,varint"`
}
type EventNotify struct{ ProtoUnknown }
type EventNotifyResp struct{ ProtoUnknown }
type WxHeartBeatReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	RecvAck     uint32   `pb:"2,varint"`
}
type WxHeartBeatResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type DevHeartBeatReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	RecvAck     uint32   `pb:"2,varint"`
}
type DevHeartBeatResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type RoomInfo struct {
	ProtoUnknown
	JoinRoom      bool   `pb:"1,varint"`
	RoomId        string `pb:"2,string"`
	OriginalMd5   string `pb:"3,string"`
	RoomStatus    uint32 `pb:"4,varint"`
	WxConnStatus  uint32 `pb:"5,varint"`
	DevConnStatus uint32 `pb:"6,varint"`
}
type WxLoginReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	LoginTicket string   `pb:"2,string"`
}
type WxLoginResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
	RoomInfo     *RoomInfo `pb:"2,msg"`
}
type DevLoginReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	Newticket   string   `pb:"2,string"`
	Autodev     uint32   `pb:"3,varint"`
}
type DevLoginResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
	RoomInfo     *RoomInfo `pb:"2,msg"`
}
type WxJoinRoomReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	Username    string   `pb:"2,string"`
	RoomId      string   `pb:"3,string"`
	WxpkgInfo   string   `pb:"4,string"`
}
type WxJoinRoomResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type DevJoinRoomReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	Appid       string   `pb:"2,string"`
	RoomId      string   `pb:"3,string"`
	WxpkgInfo   string   `pb:"4,string"`
}
type DevJoinRoomResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type WxQuitRoomReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
}
type WxQuitRoomResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type DevQuitRoomReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
}
type DevQuitRoomResp struct {
	ProtoUnknown
	BaseResponse *BaseResp `pb:"1,msg"`
}
type WxSyncMessageReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	MinSeq      uint32   `pb:"2,varint"`
	MaxSeq      uint32   `pb:"3,varint"`
}
type WxSyncMessageResp struct {
	ProtoUnknown
	BaseResponse     *BaseResp           `pb:"1,msg"`
	DebugMessageList []DebugMessageProto `pb:"2,msg"`
	SendAck          uint32              `pb:"3,varint"`
}
type DevSyncMessageReq struct {
	ProtoUnknown
	BaseRequest *BaseReq `pb:"1,msg"`
	MinSeq      uint32   `pb:"2,varint"`
	MaxSeq      uint32   `pb:"3,varint"`
}
type DevSyncMessageResp struct {
	ProtoUnknown
	BaseResponse     *BaseResp           `pb:"1,msg"`
	DebugMessageList []DebugMessageProto `pb:"2,msg"`
	SendAck          uint32              `pb:"3,varint"`
}
type MethodWithArgs struct {
	ProtoUnknown
	MethodName    string   `pb:"1,string"`
	MethodArgList []string `pb:"2,string"`
}
type RegisterInterface struct {
	ProtoUnknown
	ObjName       string           `pb:"1,string"`
	ObjMethodList []MethodWithArgs `pb:"2,msg"`
}
type DeviceInfo struct {
	ProtoUnknown
	DeviceName    string  `pb:"1,string"`
	DeviceModel   string  `pb:"2,string"`
	SystemVersion string  `pb:"3,string"`
	WechatVersion string  `pb:"4,string"`
	PublibVersion uint32  `pb:"5,varint"`
	ScreenWidth   float32 `pb:"6,fixed32"`
	PixelRatio    float32 `pb:"7,fixed32"`
	UserAgent     string  `pb:"8,string"`
}
type SetupContext struct {
	ProtoUnknown
	RegisterInterface   *RegisterInterface `pb:"1,msg"`
	DeviceInfo          *DeviceInfo        `pb:"2,msg"`
	ConfigureJs         string             `pb:"3,string"`
	PublicJsMd5         string             `pb:"4,string"`
	ThreeJsMd5          string             `pb:"5,string"`
	SupportCompressAlgo uint32             `pb:"6,varint"`
}
type CallInterfaceProto struct {
	ProtoUnknown
	ObjName    string   `pb:"1,string"`
	MethodName string   `pb:"2,string"`
	MethodArgs []string `pb:"3,string"`
	CallId     uint32   `pb:"4,varint"`
}
type CallInterfaceResultProto struct {
	ProtoUnknown
	Ret       string `pb:"1,string"`
	CallId    uint32 `pb:"2,varint"`
	DebugInfo string `pb:"3,string"`
}
type EvaluateJavascriptProto struct {
	ProtoUnknown
	Script     string `pb:"1,string"`
	EvaluateId uint32 `pb:"2,varint"`
	DebugInfo  string `pb:"3,string"`
}
type EvaluateJavascriptResultProto struct {
	ProtoUnknown
	Ret        string `pb:"1,string"`
	EvaluateId uint32 `pb:"2,varint"`
}
type BreakpointProto struct {
	ProtoUnknown
	IsHit bool `pb:"1,varint"`
}
type PingProto struct {
	ProtoUnknown
	PingId  uint64 `pb:"1,varint"`
	Payload string `pb:"2,string"`
}
type PongProto struct {
	ProtoUnknown
	PingId      uint64 `pb:"1,varint"`
	NetworkType uint32 `pb:"2,varint"`
	Payload     string `pb:"3,string"`
}
type DomOp struct {
	ProtoUnknown
	Params    string `pb:"1,string"`
	WebviewId uint32 `pb:"2,varint"`
}
type DomEvent struct {
	ProtoUnknown
	Params    string `pb:"1,string"`
	WebviewId uint32 `pb:"2,varint"`
}
type NetworkDebugAPI struct {
	ProtoUnknown
	ApiName        string `pb:"1,string"`
	TaskId         string `pb:"2,string"`
	RequestHeaders string `pb:"3,string"`
	Timestamp      uint64 `pb:"4,varint"`
}
type ChromeDevtoolsProto struct {
	ProtoUnknown
	OpId        uint64 `pb:"1,varint"`
	Payload     string `pb:"2,string"`
	JscontextId string `pb:"3,string"`
}
type ChromeDevtoolsResultProto = ChromeDevtoolsProto
type AddJsContext struct {
	ProtoUnknown
	JscontextId   string `pb:"1,string"`
	JscontextName string `pb:"2,string"`
}
type RemoveJsContext struct {
	ProtoUnknown
	JscontextId string `pb:"1,string"`
}
type ConnectJsContext struct {
	ProtoUnknown
	JscontextId string `pb:"1,string"`
}
type CustomMessageProto struct {
	ProtoUnknown
	Method  string `pb:"1,string"`
	Payload string `pb:"2,string"`
	Raw     []byte `pb:"3,bytes"`
}

func MarshalMessage(v any) ([]byte, error) { return marshalValue(reflect.ValueOf(v)) }
func marshalValue(v reflect.Value) ([]byte, error) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("message must be struct")
	}
	var out []byte
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("pb")
		if tag == "" {
			continue
		}
		p := strings.Split(tag, ",")
		var no int
		fmt.Sscanf(p[0], "%d", &no)
		x := v.Field(i)
		if x.Kind() == reflect.Slice {
			if x.Type().Elem().Kind() == reflect.String || p[1] == "msg" {
				for j := 0; j < x.Len(); j++ {
					out = appendField(out, no, p[1], x.Index(j))
				}
				continue
			}
		}
		if isZero(x) {
			continue
		}
		out = appendField(out, no, p[1], x)
	}
	if unknown := v.FieldByName("UnknownFields"); unknown.IsValid() && unknown.Kind() == reflect.Slice {
		out = append(out, unknown.Bytes()...)
	}
	return out, nil
}
func isZero(v reflect.Value) bool { return v.IsZero() }
func appendField(out []byte, no int, kind string, v reflect.Value) []byte {
	var key uint64 = uint64(no << 3)
	switch kind {
	case "varint":
		putVarBytes(&out, key)
		var n uint64
		if v.Kind() >= reflect.Int && v.Kind() <= reflect.Int64 {
			n = uint64(v.Int())
		} else if v.Kind() == reflect.Bool {
			if v.Bool() {
				n = 1
			}
		} else {
			n = v.Uint()
		}
		putVarBytes(&out, n)
	case "string", "bytes", "msg":
		key |= 2
		putVarBytes(&out, key)
		var b []byte
		if kind == "msg" {
			b, _ = marshalValue(v)
		} else if kind == "string" {
			b = []byte(v.String())
		} else {
			b = v.Bytes()
		}
		putVarBytes(&out, uint64(len(b)))
		out = append(out, b...)
	case "fixed32":
		key |= 5
		putVarBytes(&out, key)
		var q [4]byte
		binary.LittleEndian.PutUint32(q[:], math.Float32bits(float32(v.Float())))
		out = append(out, q[:]...)
	}
	return out
}
func putVarBytes(out *[]byte, n uint64) {
	for n >= 128 {
		*out = append(*out, byte(n)|128)
		n >>= 7
	}
	*out = append(*out, byte(n))
}

func UnmarshalMessage(d []byte, out any) error {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("output pointer required")
	}
	return unmarshalValue(d, v.Elem())
}
func unmarshalValue(d []byte, v reflect.Value) error {
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("message must be struct")
	}
	tags := map[int][]int{}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		tag := t.Field(i).Tag.Get("pb")
		if tag != "" {
			var n int
			fmt.Sscanf(strings.Split(tag, ",")[0], "%d", &n)
			tags[n] = []int{i}
		}
	}
	for i := 0; i < len(d); {
		fieldStart := i
		key, e := readVar(d, &i)
		if e != nil {
			return e
		}
		no, w := int(key>>3), int(key&7)
		idx, ok := tags[no]
		if !ok {
			if e := skip(d, &i, w); e != nil {
				return e
			}
			if unknown := v.FieldByName("UnknownFields"); unknown.IsValid() && unknown.CanSet() && unknown.Kind() == reflect.Slice {
				unknown.SetBytes(append(unknown.Bytes(), d[fieldStart:i]...))
			}
			continue
		}
		f := v.Field(idx[0])
		kind := strings.Split(t.Field(idx[0]).Tag.Get("pb"), ",")[1]
		if kind == "varint" {
			x, e := readVar(d, &i)
			if e != nil {
				return e
			}
			if f.Kind() == reflect.Bool {
				f.SetBool(x != 0)
			} else if f.Kind() >= reflect.Int && f.Kind() <= reflect.Int64 {
				f.SetInt(int64(x))
			} else {
				f.SetUint(x)
			}
		} else if kind == "fixed32" {
			if i+4 > len(d) {
				return fmt.Errorf("short fixed32")
			}
			f.SetFloat(float64(math.Float32frombits(binary.LittleEndian.Uint32(d[i : i+4]))))
			i += 4
		} else {
			b, e := readBytes(d, &i)
			if e != nil {
				return e
			}
			if kind == "msg" {
				if f.Kind() == reflect.Slice {
					elemType := f.Type().Elem()
					if elemType.Kind() == reflect.Pointer {
						elem := reflect.New(elemType.Elem())
						if e := unmarshalValue(b, elem.Elem()); e != nil {
							return e
						}
						f.Set(reflect.Append(f, elem))
					} else {
						elem := reflect.New(elemType)
						if e := unmarshalValue(b, elem.Elem()); e != nil {
							return e
						}
						f.Set(reflect.Append(f, elem.Elem()))
					}
				} else {
					// protobufjs creates a fresh child for every singular message
					// occurrence; the last occurrence replaces the previous child.
					f.Set(reflect.New(f.Type().Elem()))
					if e := unmarshalValue(b, f.Elem()); e != nil {
						return e
					}
				}
			} else if kind == "string" {
				if f.Kind() == reflect.Slice {
					f.Set(reflect.Append(f, reflect.ValueOf(string(b))))
				} else {
					f.SetString(string(b))
				}
			} else {
				f.SetBytes(append([]byte(nil), b...))
			}
		}
	}
	return nil
}
