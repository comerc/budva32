package config

const fileName = "config.yml"

type Config struct {
	Others   map[int64]Other
	Reports  Reports
	Forwards []Forward
}

type Other struct {
	SourceTitle string
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
	WoSendCopy      bool
	// TODO: WithEdited bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
