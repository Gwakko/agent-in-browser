package scenario

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

// WriteJSON writes all reports as a JSON array.
func WriteJSON(path string, reps []*Report) error {
	data, err := json.MarshalIndent(reps, "", "  ")
	if err != nil {
		return err
	}
	return write(path, data)
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *struct{}     `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// WriteJUnit writes a JUnit XML report (one suite per scenario×browser,
// one test case per step) for CI systems.
func WriteJUnit(path string, reps []*Report) error {
	var root junitSuites
	for _, r := range reps {
		s := junitSuite{Name: fmt.Sprintf("%s [%s]", r.Scenario, r.Browser), Time: secs(r.Duration.Seconds())}
		for _, st := range r.Steps {
			name := st.Name
			if name == "" {
				name = st.Action
			}
			c := junitCase{Name: fmt.Sprintf("%02d %s", st.Index, name), Classname: r.Scenario + "." + r.Browser, Time: secs(st.Duration.Seconds())}
			switch {
			case st.Skipped:
				c.Skipped = &struct{}{}
				s.Skipped++
			case !st.OK:
				c.Failure = &junitFailure{Message: st.Error, Body: st.Error}
				if r.Screenshot != "" {
					c.Failure.Body += "\nscreenshot: " + r.Screenshot
				}
				s.Failures++
			}
			s.Cases = append(s.Cases, c)
			s.Tests++
		}
		if r.Error != "" && len(r.Steps) == 0 {
			s.Cases = append(s.Cases, junitCase{Name: "setup", Classname: r.Scenario, Failure: &junitFailure{Message: r.Error}})
			s.Tests++
			s.Failures++
		}
		root.Tests += s.Tests
		root.Failures += s.Failures
		root.Suites = append(root.Suites, s)
	}
	data, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return write(path, append([]byte(xml.Header), data...))
}

func secs(f float64) string { return fmt.Sprintf("%.3f", f) }

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
