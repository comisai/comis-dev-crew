package reporter

func (server *RuntimeServer) launchBinding() *RuntimeLaunchConfig {
	server.launchMu.RLock()
	defer server.launchMu.RUnlock()
	if server.launch == nil {
		return nil
	}
	binding := *server.launch
	return &binding
}
