package core

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
)

type CrawlerOption func(crawler *Crawler)
type CollyConfigurator func(c *colly.Collector) error
type HTTPClientConfigurator func(client *http.Client)

func WithCollyConfig(opt ...CollyConfigurator) CrawlerOption {
	return func(crawler *Crawler) {
		crawler.collyConfigrationOpt = append(crawler.collyConfigrationOpt, opt...)
	}
}

func WithOutput(writer ...io.Writer) CrawlerOption {
	return func(crawler *Crawler) {
		if crawler.Output != nil {
			outputW := append([]io.Writer{crawler.Output}, writer...)
			crawler.Output = io.MultiWriter(outputW...)
		} else {
			crawler.Output = io.MultiWriter(writer...)
		}
	}
}

func WithFilterLength(filterLength string) CrawlerOption {
	return func(crawler *Crawler) {
		lengthArgs := strings.Split(filterLength, ",")
		for _, arg := range lengthArgs {
			if i, err := strconv.Atoi(arg); err == nil {
				crawler.filterLength_slice = append(crawler.filterLength_slice, i)
			}
		}

	}
}

func WithCollyOption(options ...colly.CollectorOption) CrawlerOption {
	return func(crawler *Crawler) {
		crawler.collectorOpt = append(crawler.collectorOpt, options...)
	}
}

func WithSitemap() CrawlerOption {
	return func(crawler *Crawler) {
		crawler.sitemap = true
	}
}

func WithRobot() CrawlerOption {
	return func(crawler *Crawler) {
		crawler.robot = true
	}
}

func WithOtherSources() CrawlerOption {
	return func(crawler *Crawler) {
		crawler.othersources = true
	}
}

func WithDefaultColly(maxDepth int) CrawlerOption {
	return WithCollyOption(
		colly.Async(true),
		colly.MaxDepth(maxDepth),
		colly.IgnoreRobotsTxt(),
	)
}

func WithScope(scope string) CollyConfigurator {
	return WithRegexpFilter(scope)
}

func WithDisallowedRegexFilter(regFilter string) CollyConfigurator {
	return func(c *colly.Collector) error {
		reg, err := regexp.Compile(regFilter)
		if err != nil {
			return fmt.Errorf("failed to compile disallowedRegex filter %s: %w", regFilter, err)
		}
		if c.DisallowedURLFilters == nil {
			c.DisallowedURLFilters = make([]*regexp.Regexp, 0, 1)
		}
		c.DisallowedURLFilters = append(c.DisallowedURLFilters, reg)
		return nil
	}
}

func WithDefaultDisalowedRegexp() CollyConfigurator {
	return WithDisallowedRegexFilter(`(?i)\.(png|apng|bmp|gif|ico|cur|jpg|jpeg|jfif|pjp|pjpeg|svg|tif|tiff|webp|xbm|3gp|aac|flac|mpg|mpeg|mp3|mp4|m4a|m4v|m4p|oga|ogg|ogv|mov|wav|webm|eot|woff|woff2|ttf|otf|css)(?:\?|#|$)`)
}

func WithRegexpFilter(regFilter string) CollyConfigurator {
	return func(c *colly.Collector) error {
		reg, err := regexp.Compile(regFilter)
		if err != nil {
			return fmt.Errorf("failed to compile Regex filter %s: %w", regFilter, err)
		}
		if c.URLFilters == nil {
			c.URLFilters = make([]*regexp.Regexp, 0, 1)
		}
		c.URLFilters = append(c.URLFilters, reg)
		return nil
	}
}

func WithWhiteListDomain(whiteListDomain string) CollyConfigurator {
	return WithRegexpFilter("http(s)?://" + whiteListDomain)
}

func WithLimit(concurrent int, delay int, randomDelay int) CollyConfigurator {
	return func(c *colly.Collector) error {
		return c.Limit(&colly.LimitRule{
			DomainGlob:  "*",
			Parallelism: concurrent,
			Delay:       time.Duration(delay) * time.Second,
			RandomDelay: time.Duration(randomDelay) * time.Second,
		})
	}
}

func WithHTTPClient(client *http.Client) CollyConfigurator {
	return func(c *colly.Collector) error {
		c.SetClient(client)
		return nil
	}
}

func WithHTTPClientOpt(opt ...HTTPClientConfigurator) CollyConfigurator {
	return func(c *colly.Collector) error {
		client := &http.Client{}
		client.Transport = DefaultHTTPTransport
		for _, o := range opt {
			o(client)
		}
		return WithHTTPClient(client)(c)
	}
}

func WithBurpFile(burpFile string) CollyConfigurator {
	return func(c *colly.Collector) error {
		bF, err := os.Open(burpFile)
		if err != nil {
			return fmt.Errorf("Failed to open Burp File: %w", err)
		} else {
			rd := bufio.NewReader(bF)
			req, err := http.ReadRequest(rd)
			if err != nil {
				return fmt.Errorf("failed to Parse Raw Request in %s: %w", burpFile, err)
			} else {
				// Set cookie
				c.OnRequest(func(r *colly.Request) {
					r.Headers.Add("Cookie", GetRawCookie(req.Cookies()))
				})

				// Set headers
				c.OnRequest(func(r *colly.Request) {
					for k, v := range req.Header {
						r.Headers.Set(strings.TrimSpace(k), strings.TrimSpace(v[0]))
					}
				})

			}
		}
		return nil
	}
}

func WithCookie(cookie string) CollyConfigurator {
	return func(c *colly.Collector) error {
		c.OnRequest(func(r *colly.Request) {
			r.Headers.Add("Cookie", cookie)
		})
		return nil
	}
}

func WithHeader(headers ...string) CollyConfigurator {
	return func(c *colly.Collector) error {
		for _, h := range headers {
			headerArgs := strings.SplitN(h, ":", 2)
			headerKey := strings.TrimSpace(headerArgs[0])
			headerValue := strings.TrimSpace(headerArgs[1])
			c.OnRequest(func(r *colly.Request) {
				r.Headers.Set(headerKey, headerValue)
			})
		}
		return nil
	}
}

func WithUserAgent(randomUA string) CollyConfigurator {
	return func(c *colly.Collector) error {
		switch ua := strings.ToLower(randomUA); {
		case ua == "mobi":
			extensions.RandomMobileUserAgent(c)
		case ua == "web":
			extensions.RandomUserAgent(c)
		default:
			c.UserAgent = ua
		}
		return nil
	}
}

func WithHTTPProxy(proxy string) HTTPClientConfigurator {
	return func(client *http.Client) {
		if proxy != "" {
			Logger.Infof("Proxy: %s", proxy)
			pU, err := url.Parse(proxy)
			if err != nil {
				Logger.Error("Failed to set proxy")
			} else {
				DefaultHTTPTransport.Proxy = http.ProxyURL(pU)
				client.Transport = DefaultHTTPTransport
			}
		}
	}
}

func WithHTTPTimeout(timeout int) HTTPClientConfigurator {
	return func(client *http.Client) {
		if timeout == 0 {
			Logger.Info("Your input timeout is 0. Gospider will set it to 10 seconds")
			client.Timeout = 10 * time.Second
		} else {
			client.Timeout = time.Duration(timeout) * time.Second
		}
	}
}

func WithHTTPNoRedirect() HTTPClientConfigurator {
	return func(client *http.Client) {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			nextLocation := req.Response.Header.Get("Location")
			Logger.Debugf("Found Redirect: %s", nextLocation)
			// Allow in redirect from http to https or in same hostname
			// We just check contain hostname or not because we set URLFilter in main collector so if
			// the URL is https://otherdomain.com/?url=maindomain.com, it will reject it
			last := via[len(via)-1].URL.Hostname()
			if strings.Contains(nextLocation, last) {
				Logger.Infof("Redirecting to: %s", nextLocation)
				return nil
			}
			return http.ErrUseLastResponse
		}
	}
}
