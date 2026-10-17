package plugin

type Host struct {
	WWWRoot    string
	Data       string
	Secrets    string
	GetSetting func(string) (string, error)
	PutSetting func(string, string) error
	GetConfig  func(string) (string, error)
	PutConfig  func(string, string) error
}

func (h *Host) WWW() string        { return h.WWWRoot }
func (h *Host) DataDir() string    { return h.Data }
func (h *Host) SecretsDir() string { return h.Secrets }

func (h *Host) Setting(key string) string {
	if h == nil || h.GetSetting == nil {
		return ""
	}
	v, _ := h.GetSetting(key)
	return v
}

func (h *Host) SetSetting(key, val string) error {
	if h == nil || h.PutSetting == nil {
		return nil
	}
	return h.PutSetting(key, val)
}

func (h *Host) Config(pluginID string) string {
	if h == nil || h.GetConfig == nil {
		return "{}"
	}
	v, _ := h.GetConfig(pluginID)
	if v == "" {
		return "{}"
	}
	return v
}

func (h *Host) SetConfig(pluginID, json string) error {
	if h == nil || h.PutConfig == nil {
		return nil
	}
	return h.PutConfig(pluginID, json)
}
