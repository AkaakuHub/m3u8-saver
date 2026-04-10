package status

type Type string

const (
	Success    Type = "success"
	Archived   Type = "archived"
	Missing    Type = "missing"
	Failed     Type = "failed"
	Incomplete Type = "incomplete"
	NotFound   Type = "N/A"
)
