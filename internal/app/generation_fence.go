package app

import "github.com/Follen/miniapp-bridge/internal/cdp"

const (
	maxCDPResponseFences = cdp.DefaultMaxPending
)

type cdpResponseFence struct {
	generation uint64
}

func (a *App) addCDPResponseFencesLocked(requests []cdp.Request, generation uint64) {
	if len(requests) == 0 || a.cdpResponseFenceBlocked {
		return
	}
	if a.cdpResponseFences == nil {
		a.cdpResponseFences = make(map[string]cdpResponseFence)
	}
	for _, request := range requests {
		key, _ := cdp.IDKey(request.ID)
		if _, exists := a.cdpResponseFences[key]; !exists && len(a.cdpResponseFences) >= maxCDPResponseFences {
			clear(a.cdpResponseFences)
			a.cdpResponseFenceBlocked = true
			return
		}
		a.cdpResponseFences[key] = cdpResponseFence{generation: generation}
	}
}

func (a *App) failCurrentControllerRequestsLocked() {
	a.connMu.RLock()
	controller := a.cdpOwner
	generation := a.cdpGeneration
	a.connMu.RUnlock()

	var controllerRequests []cdp.Request
	if controller != nil && generation != 0 {
		controllerRequests = a.Requests.DrainFor("controller", generation)
	}
	if controller == nil || len(controllerRequests) == 0 {
		return
	}

	messages := make([][]byte, 0, len(controllerRequests))
	for _, request := range controllerRequests {
		body, err := marshalCDPError(request.ID, cdpServerErrorCode, ErrCDPUpstreamDisconnected.Error())
		if err != nil {
			a.reportRuntimeError("cdp-error-writer", generation, err)
			continue
		}
		messages = append(messages, body)
	}
	if len(messages) == 0 {
		return
	}
	if err := controller.SendBatch(messages); err != nil {
		a.reportRuntimeError("cdp-error-writer", generation, err)
		go func() { _ = controller.Close() }()
	}
}

func (a *App) cdpResponseFencedLocked(id any) (bool, uint64) {
	if a.cdpResponseFenceBlocked {
		return true, 0
	}
	key, _ := cdp.IDKey(id)
	fence, ok := a.cdpResponseFences[key]
	return ok, fence.generation
}

func (a *App) consumeCDPResponseFenceLocked(id any) (bool, uint64) {
	if a.cdpResponseFenceBlocked {
		return true, 0
	}
	key, _ := cdp.IDKey(id)
	fence, ok := a.cdpResponseFences[key]
	if !ok {
		return false, 0
	}
	delete(a.cdpResponseFences, key)
	return true, fence.generation
}

func (a *App) clearCDPResponseFencesLocked() {
	clear(a.cdpResponseFences)
	a.cdpResponseFenceBlocked = false
}
