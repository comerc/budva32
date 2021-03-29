package account

const ConfigFile = "config.yml"

type AccountConfig struct {
	PhoneNumber string
	Forwards    []Forward
}

type Forward struct {
	From            int64
	To              []int64
	Exclude         string
	Include         string
	IncludeSubmatch []IncludeSubmatch
	SendCopy        bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
