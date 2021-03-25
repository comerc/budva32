package account

const ConfigFile = "config.yml"

type AccountConfig struct {
	PhoneNumber string    `json:"PhoneNumber"`
	Forwards    []Forward `json:"Forwards"`
}

type Forward struct {
	From int64   `json:"From"`
	To   []int64 `json:"To"`
}
