package core

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	sitemap "github.com/oxffaa/gopher-parse-sitemap"

	"github.com/benji-bou/gospider/stringset"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

var DefaultHTTPTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout: 10 * time.Second,
		// Default is 15 seconds
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:    100,
	MaxConnsPerHost: 1000,
	IdleConnTimeout: 30 * time.Second,

	// ExpectContinueTimeout: 1 * time.Second,
	// ResponseHeaderTimeout: 3 * time.Second,
	// DisableCompression:    false,
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Renegotiation: tls.RenegotiateOnceAsClient},
}

type Crawler struct {
	C                   *colly.Collector
	LinkFinderCollector *colly.Collector
	Output              *Output

	collectorOpt         []colly.CollectorOption
	collyConfigrationOpt []CollyConfigurator

	subSet  *stringset.StringFilter
	awsSet  *stringset.StringFilter
	jsSet   *stringset.StringFilter
	urlSet  *stringset.StringFilter
	formSet *stringset.StringFilter

	site       *url.URL
	domain     string
	Input      string
	Quiet      bool
	JsonOutput bool
	length     bool
	raw        bool
	subs       bool

	filterLength_slice []int
}

type SpiderOutput struct {
	Input      string `json:"input"`
	Source     string `json:"source"`
	OutputType string `json:"type"`
	Output     string `json:"output"`
	StatusCode int    `json:"status"`
	Length     int    `json:"length"`
}

func NewCrawler(site *url.URL, opt ...CrawlerOption) *Crawler {
	domain := GetDomain(site)
	if domain == "" {
		Logger.Error("Failed to parse domain")
		os.Exit(1)
	}
	Logger.Infof("Start crawling: %s", site)

	crawler := &Crawler{
		collectorOpt:         make([]colly.CollectorOption, 0),
		collyConfigrationOpt: make([]CollyConfigurator, 0),
		site:                 site,

		Input: site.String(),

		domain:             domain,
		urlSet:             stringset.NewStringFilter(),
		subSet:             stringset.NewStringFilter(),
		jsSet:              stringset.NewStringFilter(),
		formSet:            stringset.NewStringFilter(),
		awsSet:             stringset.NewStringFilter(),
		filterLength_slice: make([]int, 0),
	}

	for _, o := range opt {
		o(crawler)
	}
	crawler.C = colly.NewCollector(crawler.collectorOpt...)
	for _, configColly := range crawler.collyConfigrationOpt {
		err := configColly(crawler, crawler.C)
		if err != nil {
			Logger.Errorf("Failed to set Limit Rule: %s", err)
			os.Exit(1)
		}
	}
	extensions.Referer(crawler.C)
	crawler.LinkFinderCollector = crawler.C.Clone()
	return crawler
}

func (crawler *Crawler) feedLinkfinder(jsFileUrl string, OutputType string, source string) {

	if !crawler.jsSet.Duplicate(jsFileUrl) {
		outputFormat := fmt.Sprintf("[%s] - %s", OutputType, jsFileUrl)

		if crawler.JsonOutput {
			sout := SpiderOutput{
				Input:      crawler.Input,
				Source:     source,
				OutputType: OutputType,
				Output:     jsFileUrl,
			}
			if data, err := jsoniter.MarshalToString(sout); err == nil {
				outputFormat = data
				fmt.Println(outputFormat)
			}

		} else if !crawler.Quiet {
			fmt.Println(outputFormat)
		}

		if crawler.Output != nil {
			crawler.Output.WriteToFile(outputFormat)
		}

		// If JS file is minimal format. Try to find original format
		if strings.Contains(jsFileUrl, ".min.js") {
			originalJS := strings.ReplaceAll(jsFileUrl, ".min.js", ".js")
			_ = crawler.LinkFinderCollector.Visit(originalJS)
		}

		// Send Javascript to Link Finder Collector
		_ = crawler.LinkFinderCollector.Visit(jsFileUrl)

	}
}

func (crawler *Crawler) StartAll(linkfinder bool, sitemap bool, robots bool, otherSource, includeSubs, includeOtherSourceResult bool) {
	var gr sync.WaitGroup
	gr.Add(1)
	go func() {
		crawler.Start(linkfinder)
		gr.Done()
	}()
	if sitemap {
		gr.Add(1)
		go func() {
			crawler.ParseSiteMap()
			gr.Done()
		}()
	}
	if robots {
		gr.Add(1)
		go func() {
			crawler.ParseRobots()
			gr.Done()
		}()
	}
	if otherSource {
		gr.Add(1)
		go func() {
			crawler.ParseOtherSources(includeSubs, includeOtherSourceResult)
			gr.Done()
		}()
	}
	gr.Wait()
	crawler.C.Wait()
	crawler.LinkFinderCollector.Wait()
}

func (crawler *Crawler) Start(linkfinder bool) {
	// Setup Link Finder
	if linkfinder {
		crawler.setupLinkFinder()
	}

	// Handle url
	crawler.C.OnHTML("[href]", func(e *colly.HTMLElement) {
		urlString := e.Request.AbsoluteURL(e.Attr("href"))
		urlString = FixUrl(crawler.site, urlString)
		if urlString == "" {
			return
		}
		if !crawler.urlSet.Duplicate(urlString) {
			outputFormat := fmt.Sprintf("[href] - %s", urlString)
			if crawler.JsonOutput {
				sout := SpiderOutput{
					Input:      crawler.Input,
					Source:     "body",
					OutputType: "form",
					Output:     urlString,
				}
				if data, err := jsoniter.MarshalToString(sout); err == nil {
					outputFormat = data
					fmt.Println(outputFormat)
				}
			} else if !crawler.Quiet {
				fmt.Println(outputFormat)
			}
			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}
			_ = e.Request.Visit(urlString)
		}
	})

	// Handle form
	crawler.C.OnHTML("form[action]", func(e *colly.HTMLElement) {
		formUrl := e.Request.URL.String()
		if !crawler.formSet.Duplicate(formUrl) {
			outputFormat := fmt.Sprintf("[form] - %s", formUrl)
			if crawler.JsonOutput {
				sout := SpiderOutput{
					Input:      crawler.Input,
					Source:     "body",
					OutputType: "form",
					Output:     formUrl,
				}
				if data, err := jsoniter.MarshalToString(sout); err == nil {
					outputFormat = data
					fmt.Println(outputFormat)
				}
			} else if !crawler.Quiet {
				fmt.Println(outputFormat)
			}
			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}

		}
	})

	// Find Upload Form
	uploadFormSet := stringset.NewStringFilter()
	crawler.C.OnHTML(`input[type="file"]`, func(e *colly.HTMLElement) {
		uploadUrl := e.Request.URL.String()
		if !uploadFormSet.Duplicate(uploadUrl) {
			outputFormat := fmt.Sprintf("[upload-form] - %s", uploadUrl)
			if crawler.JsonOutput {
				sout := SpiderOutput{
					Input:      crawler.Input,
					Source:     "body",
					OutputType: "upload-form",
					Output:     uploadUrl,
				}
				if data, err := jsoniter.MarshalToString(sout); err == nil {
					outputFormat = data
					fmt.Println(outputFormat)
				}
			} else if !crawler.Quiet {
				fmt.Println(outputFormat)
			}
			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}
		}

	})

	// Handle js files
	crawler.C.OnHTML("[src]", func(e *colly.HTMLElement) {
		jsFileUrl := e.Request.AbsoluteURL(e.Attr("src"))
		jsFileUrl = FixUrl(crawler.site, jsFileUrl)
		if jsFileUrl == "" {
			return
		}

		fileExt := GetExtType(jsFileUrl)
		if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" {
			crawler.feedLinkfinder(jsFileUrl, "javascript", "body")
		}
	})

	crawler.C.OnResponse(func(response *colly.Response) {
		respStr := DecodeChars(string(response.Body))

		if len(crawler.filterLength_slice) == 0 || !contains(crawler.filterLength_slice, len(respStr)) {

			// Verify which link is working
			u := response.Request.URL.String()
			outputFormat := fmt.Sprintf("[url] - [code-%d] - %s", response.StatusCode, u)

			if crawler.length {
				outputFormat = fmt.Sprintf("[url] - [code-%d] - [len_%d] - %s", response.StatusCode, len(respStr), u)
			}

			if crawler.JsonOutput {
				sout := SpiderOutput{
					Input:      crawler.Input,
					Source:     "body",
					OutputType: "url",
					StatusCode: response.StatusCode,
					Output:     u,
					Length:     strings.Count(respStr, "\n"),
				}
				if data, err := jsoniter.MarshalToString(sout); err == nil {
					outputFormat = data
				}
			} else if crawler.Quiet {
				outputFormat = u
			}
			fmt.Println(outputFormat)
			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}
			if InScope(response.Request.URL, crawler.C.URLFilters) {
				crawler.findSubdomains(respStr)
				crawler.findAWSS3(respStr)
			}

			if crawler.raw {
				outputFormat := fmt.Sprintf("[Raw] - \n%s\n", respStr) //PRINTCLEAN RAW for link visited only
				if !crawler.Quiet {
					fmt.Println(outputFormat)
				}
				if crawler.Output != nil {
					crawler.Output.WriteToFile(outputFormat)
				}
			}
		}
	})

	crawler.C.OnError(func(response *colly.Response, err error) {
		Logger.Debugf("Error request: %s - Status code: %v - Error: %s", response.Request.URL.String(), response.StatusCode, err)
		/*
			1xx Informational
			2xx Success
			3xx Redirection
			4xx Client Error
			5xx Server Error
		*/
		if response.StatusCode == 404 || response.StatusCode == 429 || response.StatusCode < 100 || response.StatusCode >= 500 {
			return
		}

		u := response.Request.URL.String()
		outputFormat := fmt.Sprintf("[url] - [code-%d] - %s", response.StatusCode, u)

		if crawler.JsonOutput {
			sout := SpiderOutput{
				Input:      crawler.Input,
				Source:     "body",
				OutputType: "url",
				StatusCode: response.StatusCode,
				Output:     u,
				Length:     strings.Count(DecodeChars(string(response.Body)), "\n"),
			}
			if data, err := jsoniter.MarshalToString(sout); err == nil {
				outputFormat = data
				fmt.Println(outputFormat)
			}
		} else if crawler.Quiet {
			fmt.Println(u)
		} else {
			fmt.Println(outputFormat)
		}

		if crawler.Output != nil {
			crawler.Output.WriteToFile(outputFormat)
		}
	})

	err := crawler.C.Visit(crawler.site.String())
	if err != nil {
		Logger.Errorf("Failed to start %s: %s", crawler.site.String(), err)
	}
}

func (crawler *Crawler) ParseSiteMap() {
	sitemapUrls := []string{"/sitemap.xml", "/sitemap_news.xml", "/sitemap_index.xml", "/sitemap-index.xml", "/sitemapindex.xml",
		"/sitemap-news.xml", "/post-sitemap.xml", "/page-sitemap.xml", "/portfolio-sitemap.xml", "/home_slider-sitemap.xml", "/category-sitemap.xml",
		"/author-sitemap.xml"}

	for _, path := range sitemapUrls {
		// Ignore error when that not valid sitemap.xml path
		Logger.Infof("Trying to find %s", crawler.site.String()+path)
		_ = sitemap.ParseFromSite(crawler.site.String()+path, func(entry sitemap.Entry) error {
			url := entry.GetLocation()
			crawler.outputFormat("sitemap", url, "url", "sitemap")
			_ = crawler.C.Visit(url)
			return nil
		})
	}
}

func (crawler *Crawler) ParseRobots() {
	robotsURL := crawler.site.String() + "/robots.txt"

	resp, err := http.Get(robotsURL)
	if err != nil {
		return
	}
	if resp.StatusCode == 200 {
		Logger.Infof("Found robots.txt: %s", robotsURL)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return
		}
		lines := strings.Split(string(body), "\n")

		var re = regexp.MustCompile(".*llow: ")
		for _, line := range lines {
			if strings.Contains(line, "llow: ") {
				url := re.ReplaceAllString(line, "")
				url = FixUrl(crawler.site, url)
				if url == "" {
					continue
				}
				crawler.outputFormat("robots", url, "url", "robots")
				_ = crawler.C.Visit(url)
			}
		}
	}
}

func (crawler *Crawler) ParseOtherSources(includeSubs bool, includeOtherSourceResult bool) {
	urls := OtherSources(crawler.site.Hostname(), includeSubs)
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if len(url) == 0 {
			continue
		}

		if includeOtherSourceResult {
			crawler.outputFormat("other-sources", url, "url", "other-sources")
		}
		_ = crawler.C.Visit(url)
	}
}

func (crawler *Crawler) outputFormat(source string, output string, outputType string, rawLabel string) {
	outputFormat := fmt.Sprintf("[%s] - %s", rawLabel, output)
	if crawler.JsonOutput {
		sout := SpiderOutput{
			Input:      crawler.Input,
			Source:     source,
			OutputType: outputType,
			Output:     output,
		}
		if data, err := jsoniter.MarshalToString(sout); err == nil {
			outputFormat = data
		}
	} else if crawler.Quiet {
		outputFormat = output
	}
	fmt.Println(outputFormat)
	if crawler.Output != nil {
		crawler.Output.WriteToFile(outputFormat)
	}

}

// Find subdomains from response
func (crawler *Crawler) findSubdomains(resp string) {
	subs := GetSubdomains(resp, crawler.domain)
	for _, sub := range subs {
		if !crawler.subSet.Duplicate(sub) {
			outputFormat := fmt.Sprintf("[subdomains] - %s", sub)

			if crawler.JsonOutput {
				sout := SpiderOutput{
					Input:      crawler.Input,
					Source:     "body",
					OutputType: "subdomain",
					Output:     sub,
				}
				if data, err := jsoniter.MarshalToString(sout); err == nil {
					outputFormat = data
				}
				fmt.Println(outputFormat)
			} else if !crawler.Quiet {
				outputFormat = fmt.Sprintf("[subdomains] - http://%s", sub)
				fmt.Println(outputFormat)
				outputFormat = fmt.Sprintf("[subdomains] - https://%s", sub)
				fmt.Println(outputFormat)
			}
			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}
		}
	}
}

// Find AWS S3 from response
func (crawler *Crawler) findAWSS3(resp string) {
	aws := GetAWSS3(resp)
	for _, e := range aws {
		if !crawler.awsSet.Duplicate(e) {
			crawler.outputFormat("body", e, "was", "aws-s3")
		}
	}
}

// Setup link finder
func (crawler *Crawler) setupLinkFinder() {
	crawler.LinkFinderCollector.OnResponse(func(response *colly.Response) {
		if response.StatusCode == 404 || response.StatusCode == 429 || response.StatusCode < 100 {
			return
		}

		respStr := string(response.Body)

		if len(crawler.filterLength_slice) == 0 || !contains(crawler.filterLength_slice, len(respStr)) {

			// Verify which link is working
			u := response.Request.URL.String()
			outputFormat := fmt.Sprintf("[url] - [code-%d] - %s", response.StatusCode, u)

			if crawler.length {
				outputFormat = fmt.Sprintf("[url] - [code-%d] - [len_%d] - %s", response.StatusCode, len(respStr), u)
			}
			fmt.Println(outputFormat)

			if crawler.Output != nil {
				crawler.Output.WriteToFile(outputFormat)
			}

			if InScope(response.Request.URL, crawler.C.URLFilters) {

				crawler.findSubdomains(respStr)
				crawler.findAWSS3(respStr)

				paths, err := LinkFinder(respStr)
				if err != nil {
					Logger.Error(err)
					return
				}

				currentPathURL, err := url.Parse(u)
				currentPathURLerr := false
				if err != nil {
					currentPathURLerr = true
				}

				for _, relPath := range paths {
					var outputFormat string
					// JS Regex Result
					if crawler.JsonOutput {
						sout := SpiderOutput{
							Input:      crawler.Input,
							Source:     response.Request.URL.String(),
							OutputType: "linkfinder",
							Output:     relPath,
						}
						if data, err := jsoniter.MarshalToString(sout); err == nil {
							outputFormat = data
						}
					} else if !crawler.Quiet {
						outputFormat = fmt.Sprintf("[linkfinder] - [from: %s] - %s", response.Request.URL.String(), relPath)
					}
					fmt.Println(outputFormat)

					if crawler.Output != nil {
						crawler.Output.WriteToFile(outputFormat)
					}
					rebuildURL := ""
					if !currentPathURLerr {
						rebuildURL = FixUrl(currentPathURL, relPath)
					} else {
						rebuildURL = FixUrl(crawler.site, relPath)
					}

					if rebuildURL == "" {
						continue
					}

					// Try to request JS path
					// Try to generate URLs with main site
					fileExt := GetExtType(rebuildURL)
					if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" || fileExt == ".map" {
						crawler.feedLinkfinder(rebuildURL, "linkfinder", "javascript")
					} else if !crawler.urlSet.Duplicate(rebuildURL) {

						if crawler.JsonOutput {
							sout := SpiderOutput{
								Input:      crawler.Input,
								Source:     response.Request.URL.String(),
								OutputType: "linkfinder",
								Output:     rebuildURL,
							}
							if data, err := jsoniter.MarshalToString(sout); err == nil {
								outputFormat = data
							}
						} else if !crawler.Quiet {
							outputFormat = fmt.Sprintf("[linkfinder] - %s", rebuildURL)
						}

						fmt.Println(outputFormat)

						if crawler.Output != nil {
							crawler.Output.WriteToFile(outputFormat)
						}
						_ = crawler.C.Visit(rebuildURL)
					}

					// Try to generate URLs with the site where Javascript file host in (must be in main or sub domain)

					urlWithJSHostIn := FixUrl(crawler.site, relPath)
					if urlWithJSHostIn != "" {
						fileExt := GetExtType(urlWithJSHostIn)
						if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" || fileExt == ".map" {
							crawler.feedLinkfinder(urlWithJSHostIn, "linkfinder", "javascript")
						} else {
							if crawler.urlSet.Duplicate(urlWithJSHostIn) {
								continue
							} else {

								if crawler.JsonOutput {
									sout := SpiderOutput{
										Input:      crawler.Input,
										Source:     response.Request.URL.String(),
										OutputType: "linkfinder",
										Output:     urlWithJSHostIn,
									}
									if data, err := jsoniter.MarshalToString(sout); err == nil {
										outputFormat = data
									}
								} else if !crawler.Quiet {
									outputFormat = fmt.Sprintf("[linkfinder] - %s", urlWithJSHostIn)
								}
								fmt.Println(outputFormat)

								if crawler.Output != nil {
									crawler.Output.WriteToFile(outputFormat)
								}
								_ = crawler.C.Visit(urlWithJSHostIn) //not print care for lost link
							}
						}

					}

				}

				if crawler.raw {

					outputFormat := fmt.Sprintf("[Raw] - \n%s\n", respStr) //PRINTCLEAN RAW for link visited only
					if !crawler.Quiet {
						fmt.Println(outputFormat)
					}

					if crawler.Output != nil {
						crawler.Output.WriteToFile(outputFormat)
					}
				}
			}
		}
	})
}
