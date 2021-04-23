package config

const fileName = "config.yml"

type Config struct {
	Reports  Reports
	Forwards []Forward
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
	SendCopy        bool
	SourceTitle     string
	// TODO: WithEdited bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
