package config

const fileName = "config.yml"

type Config struct {
	SourceLinks map[int64]SourceLink
	Reports     Reports
	Forwards    []Forward
}

type SourceLink struct {
	Title string
	For   []int64
}

type Reports struct {
	Template string
	For      []int64
}

type Forward struct {
	From            int64
	To              []int64
	Other           int64
	Exclude         string
	Include         string
	IncludeSubmatch []IncludeSubmatch
	SendCopy        bool
	Force           bool
	// TODO: WithEdited bool
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
