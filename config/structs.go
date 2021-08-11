package config

const filename = ".config.yml"

type Config struct {
	Answers            []int64
	ReplaceMyselfLinks map[int64]ReplaceMyselfLink
	ReplaceFragments   map[int64]map[string]string
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
	Key             string // TODO: проверка на уникальность при чтении конфига, нельзя использовать ":,"
	From            int64
	To              []int64
	Exclude         string
	Include         string
	IncludeSubmatch []IncludeSubmatch
	SendCopy        bool
	CopyOnce        bool
	Indelible       bool
	Check           int64 // то, что нашёл Exclude
	Other           int64 // то, что отсек Exclude
}

type IncludeSubmatch struct {
	Regexp string
	Group  int64
	Match  []string
}
