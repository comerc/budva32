package account

const fileName = "config.yml"

type Config struct {
	Accounts []Account
	// Matches  []Match
}

type Account struct {
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
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}

// type Match struct {
// 	Name   string
// 	Values []string
// }
