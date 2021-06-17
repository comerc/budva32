package config

const filename = ".config.yml"

type Config struct {
	ReplaceMyselfLinks map[int64]ReplaceMyselfLink
	ReplaceFragments   map[int64]map[string]string
	Answers            []int64
	Sources            map[int64]Source
	Reports            Reports
	Forwards           []Forward
}

type ReplaceMyselfLink struct {
	DeleteExternal bool
}

type Source struct {
	Sign Sign
	Link Link
}

type Sign struct {
	Title string
	For   []int64
}

type Link struct {
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
	Check           int64
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
