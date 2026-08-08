package wmpf

var messageFactories = map[string]func() any{
	"WARemoteDebug_AddJsContext":             func() any { return &AddJsContext{} },
	"WARemoteDebug_BaseReq":                  func() any { return &BaseReq{} },
	"WARemoteDebug_BaseResp":                 func() any { return &BaseResp{} },
	"WARemoteDebug_Breakpoint":               func() any { return &BreakpointProto{} },
	"WARemoteDebug_CallInterface":            func() any { return &CallInterfaceProto{} },
	"WARemoteDebug_CallInterfaceResult":      func() any { return &CallInterfaceResultProto{} },
	"WARemoteDebug_ChromeDevtools":           func() any { return &ChromeDevtoolsProto{} },
	"WARemoteDebug_ChromeDevtoolsResult":     func() any { return &ChromeDevtoolsResultProto{} },
	"WARemoteDebug_CommReq":                  func() any { return &CommReq{} },
	"WARemoteDebug_CommResp":                 func() any { return &CommResp{} },
	"WARemoteDebug_ConnectJsContext":         func() any { return &ConnectJsContext{} },
	"WARemoteDebug_CustomMessage":            func() any { return &CustomMessageProto{} },
	"WARemoteDebug_DataFormat":               func() any { return &DataFormat{} },
	"WARemoteDebug_DebugMessage":             func() any { return &DebugMessageProto{} },
	"WARemoteDebug_DevHeartBeatReq":          func() any { return &DevHeartBeatReq{} },
	"WARemoteDebug_DevHeartBeatResp":         func() any { return &DevHeartBeatResp{} },
	"WARemoteDebug_DevJoinRoomReq":           func() any { return &DevJoinRoomReq{} },
	"WARemoteDebug_DevJoinRoomResp":          func() any { return &DevJoinRoomResp{} },
	"WARemoteDebug_DevLoginReq":              func() any { return &DevLoginReq{} },
	"WARemoteDebug_DevLoginResp":             func() any { return &DevLoginResp{} },
	"WARemoteDebug_DevQuitRoomReq":           func() any { return &DevQuitRoomReq{} },
	"WARemoteDebug_DevQuitRoomResp":          func() any { return &DevQuitRoomResp{} },
	"WARemoteDebug_DevSyncMessageReq":        func() any { return &DevSyncMessageReq{} },
	"WARemoteDebug_DevSyncMessageResp":       func() any { return &DevSyncMessageResp{} },
	"WARemoteDebug_DeviceInfo":               func() any { return &DeviceInfo{} },
	"WARemoteDebug_DomEvent":                 func() any { return &DomEvent{} },
	"WARemoteDebug_DomOp":                    func() any { return &DomOp{} },
	"WARemoteDebug_EvaluateJavascript":       func() any { return &EvaluateJavascriptProto{} },
	"WARemoteDebug_EvaluateJavascriptResult": func() any { return &EvaluateJavascriptResultProto{} },
	"WARemoteDebug_EventNotify":              func() any { return &EventNotify{} },
	"WARemoteDebug_EventNotifyResp":          func() any { return &EventNotifyResp{} },
	"WARemoteDebug_MessageNotify":            func() any { return &MessageNotify{} },
	"WARemoteDebug_MessageNotifyResp":        func() any { return &MessageNotifyResp{} },
	"WARemoteDebug_MethodWithArgs":           func() any { return &MethodWithArgs{} },
	"WARemoteDebug_NetworkDebugAPI":          func() any { return &NetworkDebugAPI{} },
	"WARemoteDebug_NewSendDebugMessageReq":   func() any { return &NewSendDebugMessageReq{} },
	"WARemoteDebug_NewSendDebugMessageResp":  func() any { return &NewSendDebugMessageResp{} },
	"WARemoteDebug_Ping":                     func() any { return &PingProto{} },
	"WARemoteDebug_Pong":                     func() any { return &PongProto{} },
	"WARemoteDebug_RegisterInterface":        func() any { return &RegisterInterface{} },
	"WARemoteDebug_RemoveJsContext":          func() any { return &RemoveJsContext{} },
	"WARemoteDebug_RoomInfo":                 func() any { return &RoomInfo{} },
	"WARemoteDebug_SendDebugMessageReq":      func() any { return &SendDebugMessageReq{} },
	"WARemoteDebug_SendDebugMessageResp":     func() any { return &SendDebugMessageResp{} },
	"WARemoteDebug_SetupContext":             func() any { return &SetupContext{} },
	"WARemoteDebug_WxHeartBeatReq":           func() any { return &WxHeartBeatReq{} },
	"WARemoteDebug_WxHeartBeatResp":          func() any { return &WxHeartBeatResp{} },
	"WARemoteDebug_WxJoinRoomReq":            func() any { return &WxJoinRoomReq{} },
	"WARemoteDebug_WxJoinRoomResp":           func() any { return &WxJoinRoomResp{} },
	"WARemoteDebug_WxLoginReq":               func() any { return &WxLoginReq{} },
	"WARemoteDebug_WxLoginResp":              func() any { return &WxLoginResp{} },
	"WARemoteDebug_WxQuitRoomReq":            func() any { return &WxQuitRoomReq{} },
	"WARemoteDebug_WxQuitRoomResp":           func() any { return &WxQuitRoomResp{} },
	"WARemoteDebug_WxSyncMessageReq":         func() any { return &WxSyncMessageReq{} },
	"WARemoteDebug_WxSyncMessageResp":        func() any { return &WxSyncMessageResp{} },
}

func NewMessage(typeName string) (any, bool) {
	factory, ok := messageFactories[typeName]
	if !ok {
		return nil, false
	}
	return factory(), true
}

func MessageTypeCount() int { return len(messageFactories) }
