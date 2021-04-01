package account

const fileName = "config.yml"

type Config struct {
	PhoneNumber string
	Forwards    []Forward
}

type Forward struct {
	From            int64
	To              []int64
	Other           int64
	Exclude         string
	Include         string
	IncludeSubmatch []IncludeSubmatch
	SendCopy        bool
	WithEdited      bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
