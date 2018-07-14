package hub

type rawMessage struct {
	MsgType string                 `json:"msgType"`
	Payload map[string]interface{} `json:"payload"`
}

func (m *rawMessage) getChannelName() (string, bool) {
	c, ok := m.Payload["channel"].(string)
	return c, ok
}
