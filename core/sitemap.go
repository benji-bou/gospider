package core

import (
	"net/url"
	"sync"

	"github.com/gocolly/colly/v2"
)

func ParseSiteMap(site *url.URL, crawler *Crawler, c *colly.Collector, wg *sync.WaitGroup) {
	defer wg.Done()
	crawler.ParseSiteMap()

}
