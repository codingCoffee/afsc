package content

import _ "embed"

//go:embed testdata/all-nodes.json
var sampleDoc []byte

var sampleMedia = map[int64]MediaInfo{
	7: {ID: 7, LegacyID: 123, Filename: "chart.jpg", Version: "ab12cd34", Mime: "image/jpeg", Width: 1600, Height: 900},
	8: {ID: 8, Filename: "clip.mp4", Version: "0011aabb", Mime: "video/mp4"},
}

// SampleDoc is a document using every node and mark, for theme checks. Its media lookup is SampleMedia.
func SampleDoc() *Node {
	n, err := Parse(sampleDoc)
	if err != nil {
		panic(err)
	}
	return n
}

func SampleMedia(id int64) (MediaInfo, bool) {
	m, ok := sampleMedia[id]
	return m, ok
}
