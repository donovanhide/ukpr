package main

import (
//	"errors"
)

// scraper to grab Waitrose press releases
type WaitroseScraper struct{}

func NewWaitroseScraper() *WaitroseScraper {
	var s WaitroseScraper
	return &s
}

func (scraper *WaitroseScraper) Name() string {
	return "waitrose"
}

// fetches a list of latest press releases from Waitrose
func (scraper *WaitroseScraper) FetchList() ([]*PressRelease, error) {
	url := "http://www.waitrose.presscentre.com/content/default.aspx?NewsAreaID=2"
	sel := "#content .main .item h3 a"
	return GenericFetchList(scraper.Name(), url, sel)
}

func (scraper *WaitroseScraper) Scrape(pr *PressRelease, raw_html string) error {
	title := "#content h1"
	content := "#content .main .bodyCopy"
	// TODO: kill everything after: "-ENDS-"
	cruft := ""
	pubDate := "#content .date_release"
	return GenericScrape(scraper.Name(), pr, raw_html, title, content, cruft, pubDate)
}
