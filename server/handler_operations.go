package server

// Request IDs belong to a connection/session, rather than the listening server.
// Retain the server registry for its existing administrative CancelOperation API.
func (h *Handler) registerOperation(id int, active *activeContext) {
	h.operationsMu.Lock()
	defer h.operationsMu.Unlock()
	if h.operations == nil {
		h.operations = make(map[int]*activeContext)
	}
	h.operations[id] = active
	h.Server.registerOperation(id, active)
}

func (h *Handler) completeOperation(id int, active *activeContext) {
	h.operationsMu.Lock()
	defer h.operationsMu.Unlock()
	h.Server.completeOperation(id, active)
	if h.operations[id] == active {
		delete(h.operations, id)
	}
}

// CancelOperation cancels only an operation received by this handler. A client
// cancellation notification cannot cancel another client's equal request ID.
func (h *Handler) CancelOperation(id int) {
	h.operationsMu.Lock()
	defer h.operationsMu.Unlock()
	if active := h.operations[id]; active != nil {
		h.Server.completeOperation(id, active)
		delete(h.operations, id)
	}
}
