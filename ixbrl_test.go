package ixbrl

import (
	"encoding/xml"
	"strings"
	"testing"
)

func sampleDoc() Document {
	inst := Context{ID: "now", EntityScheme: "http://www.companieshouse.gov.uk/", EntityID: "12345678", Instant: "2027-03-31"}
	per := Context{ID: "yr", EntityScheme: "http://www.companieshouse.gov.uk/", EntityID: "12345678", Start: "2026-04-01", End: "2027-03-31"}
	gbp := Unit{ID: "GBP", Measure: "iso4217:GBP"}
	return Document{
		Title:      "Acme & Co Ltd — accounts",
		SchemaRef:  "https://xbrl.frc.org.uk/FRS-105/2023-01-01/FRS-105-2023-01-01.xsd",
		Style:      "body{font-family:serif} .total{font-weight:bold}",
		Namespaces: map[string]string{"uk-core": "http://xbrl.frc.org.uk/fr/2023-01-01/core"},
		Contexts:   []Context{inst, per},
		Units:      []Unit{gbp},
		Body: []Block{
			Heading(1, NonNumeric("uk-core:EntityName", "now", "Acme & Co Ltd")),
			Rows(
				Row{Label: "Fixed assets", Amount: Numeric("uk-core:FixedAssets", "now", "GBP", Amount{Magnitude: "1000.00"}, 2)},
				Row{Label: "Net current liabilities", Amount: Numeric("uk-core:NetCurrentAssetsLiabilities", "now", "GBP", Amount{Magnitude: "250.00", Negative: true}, 2), Emphasis: "total"},
			),
			Paragraph("muted", Text("Prepared under the micro-entity provisions.")),
		},
	}
}

func TestRenderIsWellFormedXML(t *testing.T) {
	out, err := sampleDoc().Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Parse it independently to be sure.
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("output is not well-formed XML: %v", err)
		}
	}
}

func TestFactsAndEscaping(t *testing.T) {
	out, err := sampleDoc().Render()
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		`<ix:nonFraction name="uk-core:FixedAssets" contextRef="now" unitRef="GBP" decimals="2">1000.00</ix:nonFraction>`,
		`sign="-"`,                 // the negative fact carries a sign attribute
		`>250.00</ix:nonFraction>`, // and the magnitude is positive
		`<ix:nonNumeric name="uk-core:EntityName" contextRef="now">Acme &amp; Co Ltd</ix:nonNumeric>`, // escaped
		`<xbrli:instant>2027-03-31</xbrli:instant>`,
		`<xbrli:startDate>2026-04-01</xbrli:startDate>`,
		`<xbrli:measure>iso4217:GBP</xbrli:measure>`,
		`xmlns:uk-core="http://xbrl.frc.org.uk/fr/2023-01-01/core"`,
		`xlink:href="https://xbrl.frc.org.uk/FRS-105/2023-01-01/FRS-105-2023-01-01.xsd"`,
		`<title>Acme &amp; Co Ltd — accounts</title>`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestContextValidation(t *testing.T) {
	d := Document{Contexts: []Context{{ID: "bad", EntityScheme: "s", EntityID: "1"}}} // no period
	if _, err := d.Render(); err == nil {
		t.Error("expected an error for a context with no period")
	}
}
