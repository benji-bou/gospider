package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/benji-bou/chantools"
	"github.com/benji-bou/gospider/stringset"
	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
	sitemap "github.com/oxffaa/gopher-parse-sitemap"
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
	// C                   *colly.Collector
	// LinkFinderCollector *colly.Collector
	Output io.Writer

	collectorOpt         []colly.CollectorOption
	collyConfigrationOpt []CollyConfigurator

	set *stringset.StringFilter

	sitemap            bool
	robot              bool
	othersources       bool
	filterLength_slice []int
}

func NewCrawler(opt ...CrawlerOption) *Crawler {
	crawler := &Crawler{
		collectorOpt:         make([]colly.CollectorOption, 0),
		collyConfigrationOpt: make([]CollyConfigurator, 0),
		set:                  stringset.NewStringFilter(),
		filterLength_slice:   make([]int, 0),
	}

	for _, o := range opt {
		o(crawler)
	}
	return crawler
}

func (crawler *Crawler) handleResult(c chan<- SpiderReport, output SpiderReport) {

	if output.Output == "" {
		return
	}
	if !crawler.set.Duplicate(output.Output) {
		c <- output
	}
}

func (crawler *Crawler) provisionCollector() (*colly.Collector, error) {
	c := colly.NewCollector(crawler.collectorOpt...)
	for _, configColly := range crawler.collyConfigrationOpt {
		err := configColly(c)
		if err != nil {
			return nil, fmt.Errorf("failed to configure new colly.Collector: %w", err)
		}
	}
	extensions.Referer(c)
	return c, nil
}

func (crawler *Crawler) getTarget(site string) (*url.URL, string, error) {
	target, err := url.Parse(site)
	domain := ""
	if err == nil {
		domain = GetDomain(target)
	}
	return target, domain, err
}

func (crawler *Crawler) configCollectorListener(ctx context.Context, c *colly.Collector) <-chan SpiderReport {
	return chantools.New(func(oC chan<- SpiderReport, params ...any) {

		c := params[0].(*colly.Collector)
		ctx := params[1].(context.Context)
		isDone := false
		c.OnHTML("[href]", func(e *colly.HTMLElement) {
			if isDone {
				e.Request.Abort()
				return
			}
			urlString := e.Request.AbsoluteURL(e.Attr("href"))
			oC <- SpiderReport{
				Output:     urlString,
				OutputType: Ref,
				Source:     "body",
				Input:      e.Request.URL,
			}
		})

		// Handle form
		c.OnHTML("form[action]", func(e *colly.HTMLElement) {
			if isDone {
				e.Request.Abort()
				return
			}

			formUrl := e.Request.URL.String()
			oC <- SpiderReport{
				Output:     formUrl,
				OutputType: Form,
				Source:     "body",
				Input:      e.Request.URL,
			}

		})

		// Find Upload Form
		c.OnHTML(`input[type="file"]`, func(e *colly.HTMLElement) {
			if isDone {
				e.Request.Abort()
				return
			}

			uploadUrl := e.Request.URL.String()
			oC <- SpiderReport{
				Output:     uploadUrl,
				OutputType: Upload,
				Source:     "body",
				Input:      e.Request.URL,
			}
		})

		// Handle js files
		c.OnHTML("[src]", func(e *colly.HTMLElement) {
			if isDone {
				e.Request.Abort()
				return
			}

			jsFileUrl := e.Request.AbsoluteURL(e.Attr("src"))
			oC <- SpiderReport{
				Output:     jsFileUrl,
				OutputType: Src,
				Source:     "body",
				Input:      e.Request.URL,
			}
		})

		c.OnResponse(func(response *colly.Response) {
			if isDone {
				return
			}

			respStr := DecodeChars(string(response.Body))
			if len(crawler.filterLength_slice) == 0 || !contains(crawler.filterLength_slice, len(respStr)) {
				// Verify which link is working
				u := response.Request.URL.String()
				oC <- SpiderReport{
					Output:     u,
					OutputType: Url,
					Source:     "body",
					StatusCode: response.StatusCode,
					Body:       respStr,
					Input:      response.Request.URL,
				}
			}
		})

		c.OnError(func(response *colly.Response, err error) {
			if isDone {

				return
			}

			// Logger.Debugf("Error request: %s - Status code: %v - Error: %s", response.Request.URL.String(), response.StatusCode, err)
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
			respStr := DecodeChars(string(response.Body))
			u := response.Request.URL.String()
			oC <- SpiderReport{
				Output:     u,
				OutputType: Url,
				Source:     "body",
				StatusCode: response.StatusCode,
				Body:       respStr,
				Err:        err,
				Input:      response.Request.URL,
			}
		})
		c.OnRequest(func(r *colly.Request) {
			slog.Info("new Request", "request", r.URL.String())
			if isDone {

				slog.Info("cancelling request due to end of work trigerred", "request", r.URL.String())
				r.Abort()
			}
		})
		<-ctx.Done()
		isDone = true
		c.Wait()

	}, chantools.WithParam[SpiderReport](c), chantools.WithParam[SpiderReport](ctx))

	// Handle url
}

func (crawler *Crawler) start(ctx context.Context, handleSiteIngestionBehavior func(c *colly.Collector, errC chan<- error)) (<-chan SpiderReport, <-chan error) {

	return chantools.NewWithErr(func(outputC chan<- SpiderReport, errC chan<- error, params ...any) {
		ctx := params[0].(context.Context)
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		c, err := crawler.provisionCollector()
		if err != nil {
			errC <- fmt.Errorf("failed to provision collector: %w", err)

			return
		}
		chantools.ForEach(crawler.configCollectorListener(ctx, c), func(value SpiderReport) {
			value = value.FixUrl()
			crawler.handleResult(outputC, value)
			for _, next := range value.KeepCrawling() {
				c.Visit(next)
			}
		})
		handleSiteIngestionBehavior(c, errC)
		c.Wait()
	}, chantools.WithParam[SpiderReport](ctx))

}

func (crawler *Crawler) additionalTarget(site string) []string {
	u, err := url.Parse(site)
	res := []string{}
	if err != nil {
		return res
	}
	if crawler.sitemap {
		res = append(res, crawler.parseSiteMap(u)...)
	}
	if crawler.robot {
		robotsRes, err := crawler.parseRobots(u)
		if err != nil {
			slog.Warn("additional site from robots failed", "error", err)

		} else {
			res = append(res, robotsRes...)
		}
	}
	if crawler.othersources {
		res = append(res, crawler.parseOtherSources(u)...)
	}
	return res
}

func (crawler *Crawler) StreamScrawl(ctx context.Context, siteC <-chan string) (<-chan SpiderReport, <-chan error) {

	return crawler.start(ctx, func(c *colly.Collector, errC chan<- error) {
	L:
		for {
			select {
			case s, ok := <-siteC:
				if !ok {
					break L
				}
				e := c.Visit(s)
				for _, additionalSite := range crawler.additionalTarget(s) {
					c.Visit(additionalSite)
				}
				if e != nil {
					errC <- e
				}
			case <-ctx.Done():
				break L
			}
		}

	})
}

func (crawler *Crawler) Start(site ...string) (<-chan SpiderReport, <-chan error) {
	return crawler.start(context.Background(), func(c *colly.Collector, errC chan<- error) {
		for _, s := range site {
			c.Visit(s)
			for _, additionalSite := range crawler.additionalTarget(s) {
				c.Visit(additionalSite)
			}

		}
	})
}

func (crawler *Crawler) parseSiteMap(target *url.URL) []string {
	sitemapUrls := []string{"/sitemap.xml", "/sitemap_news.xml", "/sitemap_index.xml", "/sitemap-index.xml", "/sitemapindex.xml",
		"/sitemap-news.xml", "/post-sitemap.xml", "/page-sitemap.xml", "/portfolio-sitemap.xml", "/home_slider-sitemap.xml", "/category-sitemap.xml",
		"/author-sitemap.xml"}

	res := []string{}

	for _, path := range sitemapUrls {
		sitemap.ParseFromSite(target.String()+path, func(entry sitemap.Entry) error {
			url := entry.GetLocation()
			res = append(res, url)
			return nil
		})
	}
	return res
}

func (crawler *Crawler) parseRobots(target *url.URL) ([]string, error) {
	robotsURL := target.String() + "/robots.txt"
	res := []string{}
	resp, err := http.Get(robotsURL)
	if err != nil {
		return []string{}, err
	}
	if resp.StatusCode == 200 {
		Logger.Infof("Found robots.txt: %s", robotsURL)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return []string{}, err
		}
		lines := strings.Split(string(body), "\n")

		var re = regexp.MustCompile(".*llow: ")
		for _, line := range lines {
			if strings.Contains(line, "llow: ") {
				url := re.ReplaceAllString(line, "")
				url = FixUrl(target, url)
				if url == "" {
					continue
				}
				res = append(res, url)
			}
		}
	}
	return res, nil
}

func (crawler *Crawler) parseOtherSources(target *url.URL) []string {
	urls := OtherSources(target.Hostname(), true)
	res := make([]string, 0, len(urls))
	for _, url := range urls {
		url = strings.TrimSpace(url)
		if len(url) == 0 {
			continue
		}
		res = append(res, url)
	}
	return res
}

// Setup link finder
// func (crawler *Crawler) setupLinkFinder() {
// 	crawler.LinkFinderCollector.OnResponse(func(response *colly.Response) {
// 		if response.StatusCode == 404 || response.StatusCode == 429 || response.StatusCode < 100 {
// 			return
// 		}

// 		respStr := string(response.Body)

// 		if len(crawler.filterLength_slice) == 0 || !contains(crawler.filterLength_slice, len(respStr)) {

// 			// Verify which link is working
// 			u := response.Request.URL.String()
// 			outputFormat := fmt.Sprintf("[url] - [code-%d] - %s", response.StatusCode, u)

// 			if crawler.length {
// 				outputFormat = fmt.Sprintf("[url] - [code-%d] - [len_%d] - %s", response.StatusCode, len(respStr), u)
// 			}
// 			fmt.Println(outputFormat)

// 			if crawler.Output != nil {
// 				crawler.Output.Write([]byte(outputFormat))
// 			}

// 			if InScope(response.Request.URL, crawler.C.URLFilters) {

// 				crawler.findSubdomains(respStr)
// 				crawler.findAWSS3(respStr)

// 				paths, err := LinkFinder(respStr)
// 				if err != nil {
// 					Logger.Error(err)
// 					return
// 				}

// 				currentPathURL, err := url.Parse(u)
// 				currentPathURLerr := false
// 				if err != nil {
// 					currentPathURLerr = true
// 				}

// 				for _, relPath := range paths {
// 					var outputFormat string
// 					// JS Regex Result
// 					if crawler.JsonOutput {
// 						sout := SpiderReport{
// 							Input:      crawler.Input,
// 							Source:     response.Request.URL.String(),
// 							OutputType: "linkfinder",
// 							Output:     relPath,
// 						}
// 						if data, err := jsoniter.MarshalToString(sout); err == nil {
// 							outputFormat = data
// 						}
// 					} else if !crawler.Quiet {
// 						outputFormat = fmt.Sprintf("[linkfinder] - [from: %s] - %s", response.Request.URL.String(), relPath)
// 					}
// 					fmt.Println(outputFormat)

// 					if crawler.Output != nil {
// 						crawler.Output.Write([]byte(outputFormat))
// 					}
// 					rebuildURL := ""
// 					if !currentPathURLerr {
// 						rebuildURL = FixUrl(currentPathURL, relPath)
// 					} else {
// 						rebuildURL = FixUrl(crawler.site, relPath)
// 					}

// 					if rebuildURL == "" {
// 						continue
// 					}

// 					// Try to request JS path
// 					// Try to generate URLs with main site
// 					fileExt := GetExtType(rebuildURL)
// 					if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" || fileExt == ".map" {
// 						crawler.feedLinkfinder(rebuildURL, "linkfinder", "javascript")
// 					} else if !crawler.urlSet.Duplicate(rebuildURL) {

// 						if crawler.JsonOutput {
// 							sout := SpiderReport{
// 								Input:      crawler.Input,
// 								Source:     response.Request.URL.String(),
// 								OutputType: "linkfinder",
// 								Output:     rebuildURL,
// 							}
// 							if data, err := jsoniter.MarshalToString(sout); err == nil {
// 								outputFormat = data
// 							}
// 						} else if !crawler.Quiet {
// 							outputFormat = fmt.Sprintf("[linkfinder] - %s", rebuildURL)
// 						}

// 						fmt.Println(outputFormat)

// 						if crawler.Output != nil {
// 							crawler.Output.Write([]byte(outputFormat))
// 						}
// 						_ = crawler.C.Visit(rebuildURL)
// 					}

// 					// Try to generate URLs with the site where Javascript file host in (must be in main or sub domain)

// 					urlWithJSHostIn := FixUrl(crawler.site, relPath)
// 					if urlWithJSHostIn != "" {
// 						fileExt := GetExtType(urlWithJSHostIn)
// 						if fileExt == ".js" || fileExt == ".xml" || fileExt == ".json" || fileExt == ".map" {
// 							crawler.feedLinkfinder(urlWithJSHostIn, "linkfinder", "javascript")
// 						} else {
// 							if crawler.urlSet.Duplicate(urlWithJSHostIn) {
// 								continue
// 							} else {

// 								if crawler.JsonOutput {
// 									sout := SpiderReport{
// 										Input:      crawler.Input,
// 										Source:     response.Request.URL.String(),
// 										OutputType: "linkfinder",
// 										Output:     urlWithJSHostIn,
// 									}
// 									if data, err := jsoniter.MarshalToString(sout); err == nil {
// 										outputFormat = data
// 									}
// 								} else if !crawler.Quiet {
// 									outputFormat = fmt.Sprintf("[linkfinder] - %s", urlWithJSHostIn)
// 								}
// 								fmt.Println(outputFormat)

// 								if crawler.Output != nil {
// 									crawler.Output.Write([]byte(outputFormat))
// 								}
// 								_ = crawler.C.Visit(urlWithJSHostIn) //not print care for lost link
// 							}
// 						}

// 					}

// 				}

// 				if crawler.raw {

// 					outputFormat := fmt.Sprintf("[Raw] - \n%s\n", respStr) //PRINTCLEAN RAW for link visited only
// 					if !crawler.Quiet {
// 						fmt.Println(outputFormat)
// 					}

// 					if crawler.Output != nil {
// 						crawler.Output.Write([]byte(outputFormat))
// 					}
// 				}
// 			}
// 		}
// 	})
// }
