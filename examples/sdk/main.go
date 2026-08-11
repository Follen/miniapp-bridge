package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Follen/miniapp-bridge/sdk"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := sdk.New(sdk.Options{})
	if err != nil {
		log.Fatal(err)
	}
	if err := service.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer service.Close(context.Background())

	status := service.Status()
	fmt.Printf("debug=127.0.0.1:%d cdp=127.0.0.1:%d state=%s\n", status.DebugPort, status.CDPPort, status.State)
	if contexts := service.Contexts(); len(contexts) != 0 {
		response, requestErr := service.Send(ctx, sdk.Request{
			Method: "Runtime.evaluate",
			Params: map[string]any{"expression": "1 + 1", "returnByValue": true},
			Route:  sdk.Route{JSContextID: contexts[0].ID},
		})
		fmt.Printf("response=%+v err=%v\n", response, requestErr)
	}
}
