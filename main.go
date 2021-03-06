package main

// This program runs a server which
// 1) periodically scrapes a bunch of press release sources
// 2) serves up those press releases as server side event endpoints
//
// The scraped press releases are persistant, in a sqlite db. The idea is
// that eventually it'll be set up to keep just a week or so archive, to let
// consumers have a chance to catch up if they go down for a day or two.
// (but for now, it just keeps adding to the db)
//
// Clients connect to:
//
//   http://<host>:<port>/<source>/
//
// where source is (currently) one of:
//   tesco
//   72point
//
// Clients can send a last-event-id header to access archived press releases.
// eg:
//  $ curl http://localhost:9998/72point/ -H "Last-Event-ID: 0"
// Will serve up _all_ the stored 72point press releases.
//
// Without last-event-id, the client will be served only new press
// releases as they come in.
//
//
// TODOs
// - proper logging and error handling (kill all the panics!)
// - split up into separate packages (in particular, make it easy to build
//   a new app with a different bunch of scrapers)
// - we've already got a http server running, so should implement a simple
//   browsing interface for visual sanity-checking of press releases.
// - add a html-scrubbing func to clean up extracted content (remove style
//   attrs, ids, dodgy elements etc)

import (
	"fmt"
	"github.com/donovanhide/eventsource"
	//	"github.com/gorilla/mux"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"
)

// TODO: support multiple urls
type PressRelease struct {
	Title     string
	Source    string
	Permalink string
	PubDate   time.Time
	Content   string
	// if this is a fully-filled out press release, complete is set
	complete bool
}

// Scraper is the interface to implement to add a new scraper to the system
type Scraper interface {
	Name() string

	// Fetch a list of 'current' press releases.
	// (via RSS feed, or by scraping an index page or whatever)
	// The results are passed back as PressRelease structs. At the very least,
	// the Permalink field must be set to the URL of the press release,
	// But there's no reason FetchList() can't fill out all the fields if the
	// data is available (eg some rss feeds have everything required).
	// For incomplete PressReleases, the framework will fetch the HTML from
	// the Permalink URL, and invoke Scrape() to complete the data.
	FetchList() ([]*PressRelease, error)

	// scrape a single press release from raw html passed in as a string
	Scrape(*PressRelease, string) error
}

// helper to fetch and scrape an individual press release
func scrape(scraper Scraper, pr *PressRelease) error {
	resp, err := http.Get(pr.Permalink)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	html, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// TODO: collect redirects

	err = scraper.Scrape(pr, string(html))
	if err != nil {
		return err
	}
	return nil
}

// run a scraper
func doit(scraper Scraper, store *Store, sseSrv *eventsource.Server) {

	pressReleases, err := scraper.FetchList()
	if err != nil {
		panic(err)
	}

	// cull out the ones we've already got
	oldCount := len(pressReleases)
	pressReleases = store.WhichAreNew(pressReleases)
	log.Printf("%s: %d releases (%d new)", scraper.Name(), oldCount, len(pressReleases))
	// for all the new ones:
	for _, pr := range pressReleases {
		if !pr.complete {
			err = scrape(scraper, pr)
			if err != nil {
				log.Printf("ERROR '%s' %s\n", err, pr.Permalink)
				continue
			}
			pr.complete = true
		}

		// stash the new press release
		ev := store.Stash(pr)
		log.Printf("%s: stashed %s", scraper.Name(), pr.Permalink)

		// broadcast it to any connected clients
		sseSrv.Publish([]string{pr.Source}, ev)
	}
}

var port = flag.Int("port", 9998, "port to run server on")
var interval = flag.Int("interval", 60*10, "interval at which to poll source sites for new releases (in seconds)")
var testScraper = flag.String("t", "", "Test an individual scraper")
var briefFlag = flag.Bool("b", false, "Brief (testing mode output)")
var listFlag = flag.Bool("l", false, "List scrapers")

func main() {
	flag.Parse()

	scrapers := make(map[string]Scraper)

	foo := [...]Scraper{
		NewTescoScraper(),
		NewSeventyTwoPointScraper(),
		NewAsdaScraper(),
		NewWaitroseScraper(),
		NewMarksAndSpencerScraper(),
		NewSainsburysScraper(),
		NewMorrisonsScraper(),
		NewCooperativeScraper(),
	}
	for _, scraper := range foo {
		name := scraper.Name()
		scrapers[name] = scraper
	}

	if *listFlag {
		for name, _ := range scrapers {
			fmt.Println(name)
		}
		return
	}

	if *testScraper != "" {
		// run a single scraper, without server or store
		scraper, ok := scrapers[*testScraper]
		if !ok {
			log.Fatal("Unknown scraper")
		}
		pressReleases, err := scraper.FetchList()
		if err != nil {
			panic(err)
		}
		for _, pr := range pressReleases {
			if !pr.complete {
				log.Printf("%s: scrape %s", scraper.Name(), pr.Permalink)
				err = scrape(scraper, pr)
				if err != nil {
					log.Printf("ERROR '%s' %s\n", err, pr.Permalink)
					continue
				}
				pr.complete = true
			}

			if !*briefFlag {
				fmt.Printf("%s\n %s\n %s\n", pr.Title, pr.PubDate, pr.Permalink)
				fmt.Println("")
				fmt.Println(pr.Content)
				fmt.Println("------------------------------")
			} else {
				fmt.Printf("%s %s\n", pr.Title, pr.Permalink)
			}
		}
		return
	}

	// set up as server
	// using a common store for all scrapers
	// but no reason they couldn't all have their own store
	store := NewStore("./prstore.db")
	sseSrv := eventsource.NewServer()
	for name, _ := range scrapers {
		sseSrv.Register(name, store)
		http.Handle("/"+name+"/", sseSrv.Handler(name))
	}

	//
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		panic(err) //glog.Fatal(err)
	}
	defer l.Close()

	// cheesy task to periodically run the scrapers
	go func() {
		for {
			for _, scraper := range scrapers {
				doit(scraper, store, sseSrv)
			}
			time.Sleep(time.Duration(*interval) * time.Second)
		}
	}()

	log.Printf("running on port %d", *port)
	http.Serve(l, nil)
}
