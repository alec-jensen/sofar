package importer

import (
	"strings"
	"testing"

	"github.com/alec-jensen/sofar/internal/civil"
	"github.com/alec-jensen/sofar/internal/money"
)

func date(s string) civil.Date {
	d, err := civil.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestS19_5_CSVSignedAmountColumn(t *testing.T) {
	src := "Date,Description,Amount,Reference\n" +
		"10/01/2026,SQ *BLUE BOTTLE COFFEE,-4.50,ref1\n" +
		"2026-10-02,PAYROLL ACME,\"1,234.56\",ref2\n" +
		"10/03/2026,\"REFUND, STORE\",(12.34),\n" +
		"bad date,X,1.00,\n" +
		"10/04/2026,BAD AMOUNT,abc,\n" +
		"\n"
	got, err := ParseCSV(strings.NewReader(src), Mapping{HasHeader: true, Date: "Date", Description: "Description", Amount: "Amount", ID: "Reference"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{ProviderID: "ref1", Date: date("2026-10-01"), Amount: -450, Description: "SQ *BLUE BOTTLE COFFEE"},
		{ProviderID: "ref2", Date: date("2026-10-02"), Amount: 123456, Description: "PAYROLL ACME"},
		{Date: date("2026-10-03"), Amount: -1234, Description: "REFUND, STORE"},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("rows = %+v", got.Rows)
	}
	for i := range want {
		if got.Rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got.Rows[i], want[i])
		}
	}
	if len(got.Skipped) != 2 || got.Skipped[0].Line != 5 || got.Skipped[1].Line != 6 {
		t.Errorf("bad rows should be reported with their line numbers: %+v", got.Skipped)
	}
}

func TestS19_5_CSVDebitCreditAndInvert(t *testing.T) {
	src := "Posted,Memo,Withdrawal,Deposit\n10/01/2026,RENT,1500.00,\n10/02/2026,PAY,,2000.00\n"
	got, err := ParseCSV(strings.NewReader(src), Mapping{HasHeader: true, Date: "Posted", Description: "Memo", Debit: "Withdrawal", Credit: "Deposit"})
	if err != nil || len(got.Rows) != 2 || got.Rows[0].Amount != -150000 || got.Rows[1].Amount != 200000 {
		t.Fatalf("%+v %v", got, err)
	}
	src = "1/2/26;COFFEE;4.50\n"
	got, err = ParseCSV(strings.NewReader(src), Mapping{Delimiter: ";", Date: "1", Description: "2", Amount: "3", Invert: true})
	if err != nil || len(got.Rows) != 1 || got.Rows[0].Amount != -450 || got.Rows[0].Date != date("2026-01-02") {
		t.Fatalf("card export with positive spending and no header: %+v %v", got, err)
	}
}

func TestS19_5_CSVMappingErrors(t *testing.T) {
	if _, err := ParseCSV(strings.NewReader("a,b\n1,2\n"), Mapping{HasHeader: true, Date: "Date", Description: "b", Amount: "a"}); err == nil {
		t.Error("an unknown column must be an error")
	}
	if _, err := ParseCSV(strings.NewReader("a,b\n"), Mapping{HasHeader: true, Date: "a", Description: "b"}); err == nil {
		t.Error("a mapping without an amount must be an error")
	}
	big := strings.Repeat("x", MaxBytes+1)
	if _, err := ParseCSV(strings.NewReader(big), Mapping{Date: "1", Description: "2", Amount: "3"}); err != ErrTooLarge {
		t.Errorf("oversized file: %v", err)
	}
	if got, err := ParseCSV(strings.NewReader(string([]byte{0xEF, 0xBB, 0xBF})+"Date,Description,Amount\n2026-10-01,A,1\n"), Mapping{HasHeader: true, Date: "Date", Description: "Description", Amount: "Amount"}); err != nil || len(got.Rows) != 1 {
		t.Errorf("a UTF-8 BOM must be tolerated: %+v %v", got, err)
	}
}

func TestS19_5_GuessMapping(t *testing.T) {
	m := GuessMapping([]string{"Posting Date", "Description", "Debit", "Credit", "Balance"})
	if m.Date != "Posting Date" || m.Description != "Description" || m.Debit != "Debit" || m.Credit != "Credit" || m.Amount != "" {
		t.Errorf("%+v", m)
	}
	m = GuessMapping([]string{"Date", "Payee", "Amount"})
	if m.Amount != "Amount" || m.Description != "Payee" {
		t.Errorf("%+v", m)
	}
}

const ofxSGML = `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX>
<BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>USD
<BANKACCTFROM><BANKID>1234<ACCTID>9876<ACCTTYPE>CHECKING</BANKACCTFROM>
<BANKTRANLIST>
<STMTTRN>
<TRNTYPE>DEBIT
<DTPOSTED>20261001120000.000[-5:CDT]
<TRNAMT>-45.67
<FITID>2026100100001
<NAME>SQ *BLUE BOTTLE
<MEMO>POS PURCHASE &amp; TIP
</STMTTRN>
<STMTTRN>
<TRNTYPE>CREDIT
<DTPOSTED>20261002
<TRNAMT>1500.00
<FITID>2026100200002
<NAME>PAYROLL
<STMTTRN>
<DTPOSTED>garbage
<TRNAMT>1.00
<FITID>x
</BANKTRANLIST>
</STMTRS></STMTTRNRS></BANKMSGSRSV1>
</OFX>`

const ofxXML = `<?xml version="1.0"?><?OFX OFXHEADER="200" VERSION="220"?>
<OFX><CREDITCARDMSGSRSV1><CCSTMTTRNRS><CCSTMTRS><BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT</TRNTYPE><DTPOSTED>20261003</DTPOSTED><TRNAMT>-12.00</TRNAMT><FITID>A1</FITID><NAME>TARGET</NAME></STMTTRN>
<STMTTRN><TRNTYPE>DEBIT</TRNTYPE><DTPOSTED>20261004</DTPOSTED><TRNAMT>-7.25</TRNAMT><FITID>A2</FITID><PAYEE>HEB</PAYEE><MEMO>groceries</MEMO></STMTTRN>
</BANKTRANLIST></CCSTMTRS></CCSTMTTRNRS></CREDITCARDMSGSRSV1></OFX>`

func TestS19_5_OFXSGMLAndXMLParse(t *testing.T) {
	got, err := ParseOFX(strings.NewReader(ofxSGML))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || len(got.Skipped) != 1 {
		t.Fatalf("sgml: rows=%d skipped=%d %+v", len(got.Rows), len(got.Skipped), got)
	}
	if r := got.Rows[0]; r.ProviderID != "2026100100001" || r.Date != date("2026-10-01") || r.Amount != -4567 || r.Description != "SQ *BLUE BOTTLE" || r.Memo != "POS PURCHASE & TIP" {
		t.Errorf("first row: %+v", r)
	}
	if r := got.Rows[1]; r.Amount != 150000 || r.Description != "PAYROLL" || r.Date != date("2026-10-02") {
		t.Errorf("second row: %+v", r)
	}
	got, err = ParseOFX(strings.NewReader(ofxXML))
	if err != nil || len(got.Rows) != 2 {
		t.Fatalf("xml: %+v %v", got, err)
	}
	if got.Rows[1].Payee != "HEB" || got.Rows[1].Description != "groceries" {
		t.Errorf("payee and memo: %+v", got.Rows[1])
	}
	if got.Rows[0].Amount != money.Cents(-1200) {
		t.Errorf("amount: %+v", got.Rows[0])
	}
	if _, err = ParseOFX(strings.NewReader("this is not an ofx file")); err == nil {
		t.Error("a non-OFX file must be rejected")
	}
}

const qifSample = `!Type:Bank
D10/01/2026
T-45.67
PBLUE BOTTLE COFFEE
MPOS
N1001
^
D1/2'26
T1,500.00
PPAYROLL
^
Dnot a date
T1.00
^
`

func TestS19_5_QIFParse(t *testing.T) {
	got, err := ParseQIF(strings.NewReader(qifSample), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || len(got.Skipped) != 1 {
		t.Fatalf("%+v", got)
	}
	if r := got.Rows[0]; r.Date != date("2026-10-01") || r.Amount != -4567 || r.Description != "BLUE BOTTLE COFFEE" || r.Memo != "POS" || r.ProviderID != "1001" {
		t.Errorf("first: %+v", r)
	}
	if r := got.Rows[1]; r.Date != date("2026-01-02") || r.Amount != 150000 {
		t.Errorf("apostrophe year and thousands separator: %+v", r)
	}
	if day, _ := ParseQIF(strings.NewReader("!Type:Bank\nD13/01/2026\nT1\n^\n"), true); len(day.Rows) != 1 || day.Rows[0].Date != date("2026-01-13") {
		t.Errorf("day-first dates: %+v", day)
	}
	if _, err = ParseQIF(strings.NewReader("hello\nworld\n"), false); err == nil {
		t.Error("a file without QIF records must be rejected")
	}
}

func TestS19_5_FieldsAreCleaned(t *testing.T) {
	got, _ := ParseCSV(strings.NewReader("2026-10-01,\"A\x00B\x07  C\",1\n"), Mapping{Date: "1", Description: "2", Amount: "3"})
	if len(got.Rows) != 1 || got.Rows[0].Description != "AB C" {
		t.Errorf("control characters and runs of space are stripped: %+v", got.Rows)
	}
	long := strings.Repeat("x", 5000)
	got, _ = ParseCSV(strings.NewReader("2026-10-01,"+long+",1\n"), Mapping{Date: "1", Description: "2", Amount: "3"})
	if len(got.Rows[0].Description) != maxFieldN {
		t.Errorf("long fields are capped, got %d", len(got.Rows[0].Description))
	}
}

func FuzzParseCSV(f *testing.F) {
	f.Add("Date,Description,Amount\n2026-10-01,A,1.00\n")
	f.Add("\"unterminated,1,2\n")
	f.Fuzz(func(t *testing.T, s string) {
		ParseCSV(strings.NewReader(s), Mapping{HasHeader: true, Date: "Date", Description: "Description", Amount: "Amount"})
		ParseCSV(strings.NewReader(s), Mapping{Date: "1", Description: "2", Debit: "3", Credit: "4"})
	})
}

func FuzzParseOFX(f *testing.F) {
	f.Add(ofxSGML)
	f.Add(ofxXML)
	f.Add("<STMTTRN><DTPOSTED>2026")
	f.Fuzz(func(t *testing.T, s string) { ParseOFX(strings.NewReader(s)) })
}

func FuzzParseQIF(f *testing.F) {
	f.Add(qifSample)
	f.Add("D\nT\n^\n")
	f.Fuzz(func(t *testing.T, s string) {
		ParseQIF(strings.NewReader(s), false)
		ParseQIF(strings.NewReader(s), true)
	})
}
