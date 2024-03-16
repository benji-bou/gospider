package core

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/benji-bou/chantools"
	"golang.org/x/net/publicsuffix"
)

type OutputType string

var (
	Ref    OutputType = "ref"
	Src    OutputType = "src"
	Upload OutputType = "upload-form"
	Form   OutputType = "form"
	Url    OutputType = "url"
	S3     OutputType = "aws-s3"
	Domain OutputType = "domain"
)

func (ot OutputType) FixUrl(mainUrl *url.URL, newLoc string) string {
	return FixUrl(mainUrl, newLoc)
}

func (ot OutputType) KeepCrawling() func(value SpiderReport) []string {
	defaultCB := func(v SpiderReport) []string { return []string{} }
	switch ot {
	case Ref:
		return func(v SpiderReport) []string { return []string{v.Output} }
	// case Url:
	// return func(v SpiderReport) []string { return []string{v.Output} }
	case Src:
		return func(v SpiderReport) []string {
			res := []string{}
			fileExt := GetExtType(v.Output)
			if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" {
				res = append(res, v.Output)
				if strings.Contains(v.Output, ".min.js") {
					originalJS := strings.ReplaceAll(v.Output, ".min.js", ".js")
					res = append(res, originalJS)
				}
			}
			return res
		}
	default:
		return defaultCB
	}
}

type SpiderReport struct {
	Output     string     `json:"output" pp:"Output"`
	OutputType OutputType `json:"type" pp:"Type"`
	StatusCode int        `json:"status" pp:"Status"`
	Source     string     `json:"source" pp:"Source"`
	Body       string     `json:"-" pp:"-"`
	Err        error
	Input      *url.URL `json:"input"`
	Length     int      `json:"length"`
}

func (ov SpiderReport) FixUrl() SpiderReport {
	ov.Output = ov.OutputType.FixUrl(ov.Input, ov.Output)
	return ov
}

// SubdomainsDerivatedValues: search for subdomains in the body of the SpiderReport receiver
// if body is empty, no search are performed
// the resulting Outputs values are clone of reveiver execpt for the output which will be the fqdn found and outputType will be set to `Domain`
func (ov SpiderReport) SubdomainsDerivatedValues() ([]SpiderReport, error) {
	res := []SpiderReport{}
	if len(ov.Body) > 0 {
		topDomain, err := publicsuffix.EffectiveTLDPlusOne(ov.Input.Hostname())
		if err != nil {
			return res, fmt.Errorf("failed fetching subdomains derivated value for %s %s: %w", ov.OutputType, ov.Output, err)
		}
		for _, fqdn := range GetSubdomains(ov.Body, topDomain) {
			res = append(res, SpiderReport{
				Output:     fqdn,
				OutputType: Domain,
				Source:     ov.Source,
				Body:       ov.Body,
				StatusCode: ov.StatusCode,
				Input:      ov.Input,
			})
		}
	}
	return res, nil
}
func (ov SpiderReport) AwsS3DerivatedValues() ([]SpiderReport, error) {
	res := []SpiderReport{}
	if len(ov.Body) > 0 {
		for _, s3 := range GetAWSS3(ov.Body) {
			res = append(res, SpiderReport{
				Output:     s3,
				OutputType: S3,
				Source:     ov.Source,
				Body:       ov.Body,
				StatusCode: ov.StatusCode,
				Input:      ov.Input,
			})
		}
	}
	return res, nil
}

func (ov SpiderReport) DerivatedValues() ([]SpiderReport, error) {
	subDomains, err := ov.SubdomainsDerivatedValues()
	if err != nil {
		return nil, err
	}
	awsS3, err := ov.AwsS3DerivatedValues()
	if err != nil {
		return subDomains, err
	}

	return append(subDomains, awsS3...), nil
}

func (ov SpiderReport) AsyncDerivatedValues() (<-chan []SpiderReport, <-chan error) {
	return chantools.NewWithErr(func(c chan<- []SpiderReport, eC chan<- error, params ...any) {
		res, err := ov.DerivatedValues()
		if err != nil {
			eC <- err
			return
		}
		c <- res
	})
}

func (ov SpiderReport) KeepCrawling() []string {
	return ov.OutputType.KeepCrawling()(ov)
}
