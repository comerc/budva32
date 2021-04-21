package config

const fileName = "config.yml"

type Config struct {
	Forwards []Forward
	Reports  Reports
}

type Reports struct {
	To       []int64
	Template string
}

type Forward struct {
	From            int64
	To              []int64
	Other           int64
	Exclude         string
	Include         string
	IncludeSubmatch []IncludeSubmatch
	SourceTitle     string
	SendCopy        bool
	// WithEdited      bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
