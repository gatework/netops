package report_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/report"
)

func doc() report.Document {
	tbl := &report.Table{Columns: []string{"device", "version"}}
	tbl.Add("leaf|1", "9.3(10)")
	tbl.Add("=cmd()", "")
	return report.Document{
		Title: "软件版本", Generated: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC),
		Summary: []report.KV{{Key: "devices", Value: "2"}},
		Sections: []report.Section{
			{Title: "inventory", Table: tbl},
			{Title: "findings", Findings: []assess.Finding{{Rule: "lifecycle.below_minimum", Severity: assess.SeverityHigh,
				Subject: assess.NewSubject("device", "leaf1"), Title: "below minimum"}}},
		},
	}
}

func TestRenderers(t *testing.T) {
	t.Parallel()
	var md, txt, csvOut, js strings.Builder
	if err := report.Markdown(&md, doc()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), `leaf\|1`) || !strings.Contains(md.String(), "| high | lifecycle.below\\_minimum |") {
		t.Errorf("markdown:\n%s", md.String())
	}
	_ = report.Text(&txt, doc())
	if !strings.Contains(txt.String(), "below minimum") {
		t.Errorf("text:\n%s", txt.String())
	}
	_ = report.CSV(&csvOut, doc().Sections[0].Table)
	if !strings.Contains(csvOut.String(), "'=cmd()") {
		t.Errorf("formula not neutralized:\n%s", csvOut.String())
	}
	_ = report.JSON(&js, report.Document{Title: "x"})
	if !strings.Contains(js.String(), `"sections": []`) {
		t.Errorf("json: %s", js.String())
	}
}

func ExampleMarkdown() {
	d := report.Document{Title: "Backup", Sections: []report.Section{{Title: "changed", Text: []string{"2 devices changed"}}}}
	_ = report.Markdown(os.Stdout, d)
	// Output:
	// # Backup
	//
	// ## changed
	//
	// 2 devices changed
}
