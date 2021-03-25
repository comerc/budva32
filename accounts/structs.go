package accounts

const ConfigFile = "config.yml"

type Config struct {
	PhoneNumber string    `json:"PhoneNumber"`
	Forwards    []Forward `json:"Forwards"`
}

type Forward struct {
	From int64   `json:"From"`
	To   []int64 `json:"To"`
}
