package blocks

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	in := "intro\n<!-- wp:paragraph {\"align\":\"center\"} -->\n<p class=\"has-text-align-center\">Hi</p>\n<!-- /wp:paragraph -->\n" +
		"<!-- wp:columns --><div class=\"wp-block-columns\"><!-- wp:column --><div><!-- wp:image {\"id\":12,\"sizeSlug\":\"large\"} -->" +
		"<figure><img src=\"a.jpg\"/></figure><!-- /wp:image --></div><!-- /wp:column --></div><!-- /wp:columns -->" +
		"<!-- wp:more /-->tail<!-- wp:acme/widget {\"x\":1} /-->"
	parts := Parse(in)
	if parts[0].HTML != "intro\n" {
		t.Fatalf("freeform: %q", parts[0].HTML)
	}
	p := parts[1].Block
	if p.Name != "core/paragraph" || p.Attrs["align"] != "center" || p.InnerHTML() != "\n<p class=\"has-text-align-center\">Hi</p>\n" {
		t.Fatalf("paragraph: %+v", p)
	}
	cols := parts[3].Block
	col := cols.Inner()[0]
	img := col.Inner()[0]
	if cols.Name != "core/columns" || col.Name != "core/column" || img.Name != "core/image" || img.Attrs["id"] != float64(12) {
		t.Fatalf("nesting: %+v", cols)
	}
	if parts[4].Block.Name != "core/more" || parts[5].HTML != "tail" || parts[6].Block.Name != "acme/widget" {
		t.Fatalf("tail parts: %+v", parts[4:])
	}
	want := map[string]int{"core/paragraph": 1, "core/columns": 1, "core/column": 1, "core/image": 1, "core/more": 1, "acme/widget": 1}
	if got := Names(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("names %v", got)
	}
}

func TestUnclosedAndInvalidJSON(t *testing.T) {
	parts := Parse(`<!-- wp:quote {"bad": } --><blockquote>q</blockquote>`)
	b := parts[0].Block
	if b.Name != "core/quote" || b.Attrs["_invalid_json"] == nil || b.InnerHTML() != "<blockquote>q</blockquote>" {
		t.Fatalf("got %+v", b)
	}
	if HasBlocks("plain <p>x</p>") {
		t.Fatal("no blocks expected")
	}
}
