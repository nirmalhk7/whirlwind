package verification

import (
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
)

type baselineJUnitCase struct {
	Name     string     `xml:"name,attr"`
	Class    string     `xml:"classname,attr"`
	Failures []struct{} `xml:"failure"`
	Errors   []struct{} `xml:"error"`
}
type baselineJUnitSuite struct {
	XMLName  xml.Name
	Name     string               `xml:"name,attr"`
	Tests    *int                 `xml:"tests,attr"`
	Failures *int                 `xml:"failures,attr"`
	Errors   *int                 `xml:"errors,attr"`
	Suites   []baselineJUnitSuite `xml:"testsuite"`
	Cases    []baselineJUnitCase  `xml:"testcase"`
}

func baselineJUnit(output string) ([]baselineFinding, error) {
	decoder := xml.NewDecoder(strings.NewReader(output))
	var root baselineJUnitSuite
	if decoder.Decode(&root) != nil || (root.XMLName.Local != "testsuite" && root.XMLName.Local != "testsuites") {
		return nil, errors.New("baseline comparison blocked: malformed JUnit report")
	}
	if err := baselineJUnitTrailing(decoder); err != nil {
		return nil, err
	}
	findings, _, err := baselineJUnitFindings(root, "")
	return findings, err
}

func baselineJUnitTrailing(decoder *xml.Decoder) error {
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("baseline comparison blocked: malformed JUnit trailing content")
		}
		if chars, ok := token.(xml.CharData); !ok || strings.TrimSpace(string(chars)) != "" {
			return errors.New("baseline comparison blocked: unexpected JUnit trailing content")
		}
	}
}

func baselineJUnitFindings(suite baselineJUnitSuite, prefix string) ([]baselineFinding, int, error) {
	prefix += suite.Name + "/"
	if suite.Errors != nil && *suite.Errors != 0 {
		return nil, 0, errors.New("baseline comparison blocked: JUnit reported infrastructure errors")
	}
	findings, err := suite.caseFindings(prefix)
	if err != nil {
		return nil, 0, err
	}
	count := len(suite.Cases)
	for _, child := range suite.Suites {
		nested, tests, err := baselineJUnitFindings(child, prefix)
		if err != nil {
			return nil, 0, err
		}
		findings = append(findings, nested...)
		count += tests
	}
	if err := suite.validateCounts(count, len(findings)); err != nil {
		return nil, 0, err
	}
	return findings, count, nil
}

func (suite baselineJUnitSuite) caseFindings(prefix string) ([]baselineFinding, error) {
	findings := []baselineFinding{}
	for _, test := range suite.Cases {
		if test.Name == "" || len(test.Errors) > 0 {
			return nil, errors.New("baseline comparison blocked: invalid JUnit test or infrastructure error")
		}
		if len(test.Failures) > 0 {
			findings = append(findings, baselineFinding{identity: prefix + test.Class + "/" + test.Name, severity: 1})
		}
	}
	return findings, nil
}

func (suite baselineJUnitSuite) validateCounts(count, failures int) error {
	if suite.Tests != nil && *suite.Tests != count {
		return errors.New("baseline comparison blocked: incomplete JUnit test count")
	}
	if suite.Failures != nil && *suite.Failures != failures {
		return errors.New("baseline comparison blocked: incomplete JUnit failure count " + strconv.Itoa(failures))
	}
	if count == 0 && suite.XMLName.Local == "testsuite" {
		return errors.New("baseline comparison blocked: JUnit contains no test results")
	}
	return nil
}
