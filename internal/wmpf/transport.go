package wmpf

import (
	"bytes"
	"fmt"
)

var marshalOutgoingMessage = MarshalMessage

// Command values are the wire values from RemoteDebugConstants.js. The 100x
// range is the client side and 200x is the developer side of the same RPC.
const (
	CmdClientHeartbeat             uint32 = 1001
	CmdClientLogin                 uint32 = 1002
	CmdClientJoinRoom              uint32 = 1003
	CmdClientQuitRoom              uint32 = 1004
	CmdClientSyncMessage           uint32 = 1005
	CmdClientMessageNotify         uint32 = 2000
	CmdClientMessageNotifyParallel uint32 = 2006
	CmdClientSendDebug             uint32 = 1000
	CmdClientSendDebugParallel     uint32 = 1006
	CmdDevHeartbeat                uint32 = 2001
	CmdDevLogin                    uint32 = 2002
	CmdDevJoinRoom                 uint32 = 2003
	CmdDevQuitRoom                 uint32 = 2004
	CmdDevSyncMessage              uint32 = 2005
	CmdDevMessageNotify            uint32 = 1000
	CmdDevMessageNotifyParallel    uint32 = 1006
	CmdDevSendDebug                uint32 = 2000
	CmdDevSendDebugParallel        uint32 = 2006
	CmdEventBegin                  uint32 = 3001
	CmdEventEnd                    uint32 = 3002
	CmdEventBlock                  uint32 = 3003
)

func EncodeDataFormat(v DataFormat) ([]byte, error) { return MarshalMessage(v) }
func DecodeDataFormat(data []byte) (DataFormat, error) {
	var v DataFormat
	return v, UnmarshalMessage(data, &v)
}

// EncodeDeveloperOutgoingDataFormat mirrors RemoteDebugCodex.wrapOutgoingToProto.
// Its debug-message list intentionally emits explicit zero compression fields
// and omits the schema's `after` field, matching the reference helper d().
func EncodeDeveloperOutgoingDataFormat(cmd uint32, uuid string, value any, parallelNotify bool) ([]byte, error) {
	var payload []byte
	switch cmd {
	case CmdDevSendDebug:
		v, ok := value.(SendDebugMessageReq)
		if !ok {
			return nil, fmt.Errorf("developer send debug expects SendDebugMessageReq")
		}
		var b bytes.Buffer
		if v.BaseRequest != nil {
			x, err := marshalOutgoingMessage(v.BaseRequest)
			if err != nil {
				return nil, err
			}
			putMessage(&b, 1, x)
		}
		for _, message := range v.DebugMessageList {
			putMessage(&b, 2, encodeListedDebugMessageCodex(message))
		}
		putU(&b, 3, uint64(v.RecvAck))
		payload = b.Bytes()
	case CmdDevSendDebugParallel:
		if parallelNotify {
			v, ok := value.(MessageNotify)
			if !ok {
				return nil, fmt.Errorf("developer parallel notify expects MessageNotify")
			}
			payload = encodeMessageNotifyCodex(v.DebugMessageList)
			break
		}
		v, ok := value.(NewSendDebugMessageReq)
		if !ok {
			return nil, fmt.Errorf("developer parallel debug expects NewSendDebugMessageReq")
		}
		var b bytes.Buffer
		if v.BaseRequest != nil {
			x, err := marshalOutgoingMessage(v.BaseRequest)
			if err != nil {
				return nil, err
			}
			putMessage(&b, 1, x)
		}
		for _, message := range v.DebugMessageList {
			putMessage(&b, 2, encodeListedDebugMessageCodex(message))
		}
		putU(&b, 3, uint64(v.RecvAck))
		payload = b.Bytes()
	default:
		return nil, fmt.Errorf("unsupported developer outgoing command %d", cmd)
	}
	return encodeDataFormatCodex(cmd, uuid, payload), nil
}

// DecodeCommandPayload decodes the inner protobuf selected by a DataFormat
// command while retaining the direction-specific message type.
type EndpointRole uint8

const (
	RoleClient EndpointRole = iota
	RoleDeveloper
)

func decodePayload(data []byte, value any) (any, error) {
	if err := UnmarshalMessage(data, value); err != nil {
		return nil, err
	}
	return value, nil
}

// DecodeCommandPayload matches the two reference Codex unwrap entry points:
// client frames are requests and developer frames are responses. Unknown
// commands produce an empty object while the caller retains DataFormat.Data.
func DecodeCommandPayload(role EndpointRole, cmd uint32, data []byte) (any, error) {
	// RemoteDebugCodex.unwrapClientProtoToDataFormat only decodes the six
	// client RPC request commands; notify/event command values are represented
	// as an empty object on that path while retaining their raw Data bytes.
	if role == RoleClient && cmd != CmdClientHeartbeat && cmd != CmdClientLogin &&
		cmd != CmdClientJoinRoom && cmd != CmdClientQuitRoom &&
		cmd != CmdClientSyncMessage && cmd != CmdClientSendDebugParallel {
		return map[string]any{}, nil
	}
	var (
		value any
		err   error
	)
	if role == RoleClient {
		value, err = DecodeRequestPayload(role, cmd, data)
	} else {
		value, err = DecodeResponsePayload(role, cmd, data)
	}
	if err != nil && cmd != CmdEventBegin && cmd != CmdEventEnd && cmd != CmdEventBlock && !isKnownCommand(cmd) {
		return map[string]any{}, nil
	}
	return value, err
}

func isKnownCommand(cmd uint32) bool {
	return (cmd >= 1000 && cmd <= 1006) || (cmd >= 2000 && cmd <= 2006)
}

func DecodeRequestPayload(role EndpointRole, cmd uint32, data []byte) (any, error) {
	decode := func(v any) (any, error) {
		return decodePayload(data, v)
	}
	if (role == RoleClient && cmd == CmdClientMessageNotify) || (role == RoleDeveloper && cmd == CmdDevMessageNotify) {
		return decode(&MessageNotify{})
	}
	if (role == RoleClient && cmd == CmdClientMessageNotifyParallel) || (role == RoleDeveloper && cmd == CmdDevMessageNotifyParallel) {
		return decode(&MessageNotify{})
	}
	if role == RoleClient {
		switch cmd {
		case CmdClientHeartbeat:
			return decode(&WxHeartBeatReq{})
		case CmdClientLogin:
			return decode(&WxLoginReq{})
		case CmdClientJoinRoom:
			return decode(&WxJoinRoomReq{})
		case CmdClientQuitRoom:
			return decode(&WxQuitRoomReq{})
		case CmdClientSyncMessage:
			return decode(&WxSyncMessageReq{})
		case CmdClientSendDebug:
			return decode(&SendDebugMessageReq{})
		case CmdClientSendDebugParallel:
			return decode(&NewSendDebugMessageReq{})
		}
	} else {
		switch cmd {
		case CmdDevHeartbeat:
			return decode(&DevHeartBeatReq{})
		case CmdDevLogin:
			return decode(&DevLoginReq{})
		case CmdDevJoinRoom:
			return decode(&DevJoinRoomReq{})
		case CmdDevQuitRoom:
			return decode(&DevQuitRoomReq{})
		case CmdDevSyncMessage:
			return decode(&DevSyncMessageReq{})
		case CmdDevSendDebug:
			return decode(&SendDebugMessageReq{})
		case CmdDevSendDebugParallel:
			return decode(&NewSendDebugMessageReq{})
		}
	}
	return nil, fmt.Errorf("unknown WMPF request command %d for role %d", cmd, role)
}

func DecodeResponsePayload(role EndpointRole, cmd uint32, data []byte) (any, error) {
	decode := func(v any) (any, error) { return decodePayload(data, v) }
	if cmd == CmdEventBegin || cmd == CmdEventEnd || cmd == CmdEventBlock {
		return decode(&EventNotify{})
	}
	if role == RoleClient {
		switch cmd {
		case CmdClientHeartbeat:
			return decode(&WxHeartBeatResp{})
		case CmdClientLogin:
			return decode(&WxLoginResp{})
		case CmdClientJoinRoom:
			return decode(&WxJoinRoomResp{})
		case CmdClientQuitRoom:
			return decode(&WxQuitRoomResp{})
		case CmdClientSyncMessage:
			return decode(&WxSyncMessageResp{})
		case CmdClientSendDebug:
			return decode(&SendDebugMessageResp{})
		case CmdClientSendDebugParallel:
			return decode(&NewSendDebugMessageResp{})
		case CmdClientMessageNotify, CmdClientMessageNotifyParallel:
			return decode(&MessageNotify{})
		}
	} else {
		switch cmd {
		case CmdDevHeartbeat:
			return decode(&DevHeartBeatResp{})
		case CmdDevLogin:
			return decode(&DevLoginResp{})
		case CmdDevJoinRoom:
			return decode(&DevJoinRoomResp{})
		case CmdDevQuitRoom:
			return decode(&DevQuitRoomResp{})
		case CmdDevSyncMessage:
			return decode(&DevSyncMessageResp{})
		case CmdDevSendDebug:
			return decode(&SendDebugMessageResp{})
		case CmdDevSendDebugParallel:
			return decode(&NewSendDebugMessageResp{})
		case CmdDevMessageNotify, CmdDevMessageNotifyParallel:
			return decode(&MessageNotify{})
		}
	}
	return nil, fmt.Errorf("unknown WMPF response command %d for role %d", cmd, role)
}

func EncodeCommandPayload(role EndpointRole, cmd uint32, value any) ([]byte, error) {
	_ = role
	if (cmd >= 1000 && cmd <= 1006) || (cmd >= 2000 && cmd <= 2006) || cmd == CmdEventBegin || cmd == CmdEventEnd || cmd == CmdEventBlock {
		return MarshalMessage(value)
	}
	return nil, fmt.Errorf("unknown WMPF command %d", cmd)
}

func putMessage(buffer *bytes.Buffer, field int, data []byte) {
	putB(buffer, field, data)
}

func encodeBaseResponseCodex(value *BaseResp) []byte {
	if value == nil {
		return nil
	}
	var buffer bytes.Buffer
	putU(&buffer, 1, uint64(int64(value.Errcode)))
	putS(&buffer, 2, value.Errmsg)
	return buffer.Bytes()
}

func encodeRoomInfoCodex(value *RoomInfo) []byte {
	var buffer bytes.Buffer
	if value == nil {
		return buffer.Bytes()
	}
	join := uint64(0)
	if value.JoinRoom {
		join = 1
	}
	putU(&buffer, 1, join)
	putS(&buffer, 2, value.RoomId)
	putS(&buffer, 3, value.OriginalMd5)
	putU(&buffer, 4, uint64(value.RoomStatus))
	putU(&buffer, 5, uint64(value.WxConnStatus))
	putU(&buffer, 6, uint64(value.DevConnStatus))
	return buffer.Bytes()
}

// encodeListedDebugMessageCodex reproduces RemoteDebugCodex.d(). That helper
// passes a property named delay to protobufjs while the schema field is after,
// so field 2 is deliberately absent from client response message lists.
func encodeListedDebugMessageCodex(value DebugMessageProto) []byte {
	var buffer bytes.Buffer
	putU(&buffer, 1, uint64(value.Seq))
	putS(&buffer, 3, value.Category)
	putB(&buffer, 4, value.Data)
	putU(&buffer, 5, uint64(value.CompressAlgo))
	putU(&buffer, 6, uint64(value.OriginalSize))
	return buffer.Bytes()
}

func encodeMessageNotifyCodex(messages []DebugMessageProto) []byte {
	var buffer bytes.Buffer
	for _, message := range messages {
		putMessage(&buffer, 1, encodeListedDebugMessageCodex(message))
	}
	return buffer.Bytes()
}

func encodeDataFormatCodex(cmd uint32, uuid string, data []byte) []byte {
	var buffer bytes.Buffer
	putU(&buffer, 1, uint64(cmd))
	putS(&buffer, 2, uuid)
	putB(&buffer, 3, data)
	return buffer.Bytes()
}

// EncodeClientResponseDataFormat mirrors wrapClientResponseDataFormatToProto.
func EncodeClientResponseDataFormat(cmd uint32, uuid string, value any) ([]byte, error) {
	var payload []byte
	switch cmd {
	case CmdClientLogin:
		response, ok := value.(WxLoginResp)
		if !ok {
			return nil, fmt.Errorf("client login response expects WxLoginResp")
		}
		var buffer bytes.Buffer
		putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		putMessage(&buffer, 2, encodeRoomInfoCodex(response.RoomInfo))
		payload = buffer.Bytes()
	case CmdClientHeartbeat:
		response, ok := value.(WxHeartBeatResp)
		if !ok {
			return nil, fmt.Errorf("client heartbeat response expects WxHeartBeatResp")
		}
		var buffer bytes.Buffer
		if response.BaseResponse != nil {
			putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		}
		payload = buffer.Bytes()
	case CmdClientJoinRoom:
		response, ok := value.(WxJoinRoomResp)
		if !ok {
			return nil, fmt.Errorf("client join response expects WxJoinRoomResp")
		}
		var buffer bytes.Buffer
		if response.BaseResponse != nil {
			putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		}
		payload = buffer.Bytes()
	case CmdClientQuitRoom:
		response, ok := value.(WxQuitRoomResp)
		if !ok {
			return nil, fmt.Errorf("client quit response expects WxQuitRoomResp")
		}
		var buffer bytes.Buffer
		if response.BaseResponse != nil {
			putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		}
		payload = buffer.Bytes()
	case CmdClientSyncMessage:
		response, ok := value.(WxSyncMessageResp)
		if !ok {
			return nil, fmt.Errorf("client sync response expects WxSyncMessageResp")
		}
		var buffer bytes.Buffer
		if response.BaseResponse != nil {
			putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		}
		for _, message := range response.DebugMessageList {
			putMessage(&buffer, 2, encodeListedDebugMessageCodex(message))
		}
		putU(&buffer, 3, uint64(response.SendAck))
		payload = buffer.Bytes()
	case CmdClientSendDebugParallel:
		response, ok := value.(NewSendDebugMessageResp)
		if !ok {
			return nil, fmt.Errorf("client parallel debug response expects NewSendDebugMessageResp")
		}
		var buffer bytes.Buffer
		if response.BaseResponse != nil {
			putMessage(&buffer, 1, encodeBaseResponseCodex(response.BaseResponse))
		}
		putU(&buffer, 2, uint64(response.MinAck))
		putU(&buffer, 3, uint64(response.MaxAck))
		payload = buffer.Bytes()
	case CmdClientMessageNotifyParallel:
		notify, ok := value.(MessageNotify)
		if !ok {
			return nil, fmt.Errorf("client parallel notify expects MessageNotify")
		}
		payload = encodeMessageNotifyCodex(notify.DebugMessageList)
	case CmdEventBegin, CmdEventEnd, CmdEventBlock:
		if _, ok := value.(EventNotify); !ok {
			return nil, fmt.Errorf("client event response expects EventNotify")
		}
		payload = []byte{}
	default:
		return nil, fmt.Errorf("error wrapping outgoing object, invalid type %d", cmd)
	}
	return encodeDataFormatCodex(cmd, uuid, payload), nil
}
