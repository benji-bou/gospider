package main

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/benji-bou/gospider/core"
	"github.com/k0kubun/pp/v3"
)

func initSiteToScrawl(siteList []string) <-chan string {
	inputC := make(chan string)
	go func(siteList []string) {
		for _, s := range siteList {
			inputC <- s
		}
	}(siteList)
	return inputC
}

func main() {
	siteList := []string{"https://google.com"}
	ctx, _ := context.WithDeadline(context.Background(), time.Now().Add(5*time.Second))

	crawler := NewCrawler(siteList)
	outputC, errC := crawler.StreamScrawl(ctx, initSiteToScrawl(siteList))

	for {
		select {
		case res, ok := <-outputC:
			if !ok {
				return
			}
			pp.Println(res)

		case e, ok := <-errC:
			if !ok {
				return
			}

			slog.Error(e.Error())
		}
	}
}

func NewCrawler(siteList []string) *core.Crawler {

	scopeConfig := []core.CollyConfigurator{}
	for _, s := range siteList {
		u, e := url.Parse(s)
		if e != nil {
			continue
		}
		scopeConfig = append(scopeConfig, core.WithScope(u.Hostname()))
	}

	return core.NewCrawler(
		core.WithOtherSources(),
		core.WithSitemap(),
		core.WithRobot(),
		core.WithDefaultColly(3),
		// core.WithFilterLength(),
		core.WithCollyConfig(
			append([]core.CollyConfigurator{
				core.WithHTTPClientOpt(
					// core.WithHTTPProxy(proxy)
					core.WithHTTPTimeout(5),
					core.WithHTTPNoRedirect(),
				),
				// core.WithBurpFile(burpFile),
				// core.WithCookie(cookie),
				// core.WithHeader(headers...),
				core.WithUserAgent("mozilla"),
				// core.WithLimit(concurrent, delay, randomDelay),
				// core.WithDefaultDisalowedRegexp(),
				// core.WithDisallowedRegexFilter(blacklists),
				// core.WithRegexpFilter(whiteLists),
				// core.WithRegexpFilter(whiteListDomain),
			}, scopeConfig...)...,
		))
}
