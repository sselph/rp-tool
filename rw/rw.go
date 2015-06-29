package rw

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Op string

const (
	Start Op = "START"
	Stop  Op = "STOP"
)

type System struct {
	Name     string
	FullName string
	Platform string
	Path     string
}

type Game struct {
	XMLName     xml.Name `xml:"game" json:"-"`
	Path        string   `xml:"path"`
	GameTitle   string   `xml:"name"`
	Overview    string   `xml:"desc"`
	Image       string   `xml:"image,omitempty"`
	Thumb       string   `xml:"thumbnail,omitempty"`
	Rating      float64  `xml:"rating,omitempty"`
	ReleaseDate string   `xml:"releasedate"`
	Developer   string   `xml:"developer"`
	Publisher   string   `xml:"publisher"`
	Genre       string   `xml:"genre"`
	Players     int64    `xml:"players,omitempty"`
}

type GameListXML struct {
	XMLName  xml.Name `xml:"gameList"`
	GameList []Game   `xml:"game"`
}

type Event struct {
	Op     Op
	Time   time.Time
	System System
	Game   Game
}

type Watcher struct {
	Events   chan Event
	Errors   chan error
	done     chan struct{}
	doneResp chan struct{}
	last     Event
	systems  []*system
	home     string
}

func (w *Watcher) isClosed() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *Watcher) Close() error {
	if w.isClosed() {
		return nil
	}
	close(w.done)
	<-w.doneResp
	return nil
}

func (w *Watcher) run() {
	defer close(w.doneResp)
	defer close(w.Errors)
	defer close(w.Events)

	procs := make(map[int]time.Time)
	running := -1
	c := time.Tick(1 * time.Second)
	for now := range c {
		if w.isClosed() {
			return
		}
		if running != -1 {
			if exists(fmt.Sprintf("/proc/%d/cmdline", running)) {
				continue
			} else {
				e := w.last
				e.Op = Stop
				e.Time = now
				w.Events <- e
				w.last = e
				running = -1
			}
		}
		files, err := ioutil.ReadDir("/proc/")
		if err != nil {
			w.Errors <- err
		}
		for _, file := range files {
			if !file.IsDir() {
				continue
			}
			p, err := strconv.Atoi(file.Name())
			if err != nil {
				continue
			}
			if t, ok := procs[p]; ok && now.Before(t.Add(10*time.Minute)) {
				continue
			}
			f, err := os.Open(fmt.Sprintf("/proc/%d/cmdline", p))
			if err != nil {
				w.Errors <- err
				continue
			}
			b, err := ioutil.ReadAll(f)
			if err != nil {
				w.Errors <- err
				return
			}
			var found bool
			for _, s := range w.systems {
				m, err := s.Match(string(b))
				if err != nil {
					w.Errors <- err
					continue
				}
				if m == "" {
					continue
				}
				g, err := s.GetGame(m)
				if err != nil {
					w.Errors <- err
					g = Game{Path: m}
				}
				e := Event{
					Op:     Start,
					Time:   now,
					Game:   g,
					System: System{Name: s.Name, FullName: s.FullName, Platform: s.Platform, Path: s.Path},
				}
				w.last = e
				w.Events <- e
				running = p
				found = true
				break
			}
			if !found {
				procs[p] = now
			}
		}
	}
}

func New(home string) (*Watcher, error) {
	systems, err := getEmulators(home)
	if err != nil {
		return nil, err
	}
	for _, s := range systems {
		err := s.Init(home)
		if err != nil {
			return nil, fmt.Errorf("system.Init: %v, %v", s, err)
		}
	}
	w := &Watcher{
		Events:   make(chan Event),
		Errors:   make(chan error),
		done:     make(chan struct{}),
		doneResp: make(chan struct{}),
		systems:  systems,
		home:     home,
	}
	go w.run()
	return w, nil
}

// exists checks if a file exists and contains data.
func exists(s string) bool {
	_, err := os.Stat(s)
	return !os.IsNotExist(err)
}

type system struct {
	Name         string `xml:"name"`
	FullName     string `xml:"fullname"`
	Path         string `xml:"path"`
	Platform     string `xml:"platform"`
	Command      string `xml:"command"`
	re           *regexp.Regexp
	v            string
	skip         bool
	GameListPath string
	home         string
}

func (s *system) GetGame(game string) (Game, error) {
	var g Game
	if s.GameListPath == "" {
		return g, fmt.Errorf("no gamelist.xml")
	}
	gl := &GameListXML{}
	f, err := os.Open(s.GameListPath)
	if err != nil {
		return g, err
	}
	defer f.Close()
	decoder := xml.NewDecoder(f)
	if err := decoder.Decode(gl); err != nil {
		return g, err
	}
	name := filepath.Base(game)
	for _, x := range gl.GameList {
		gName := filepath.Base(x.Path)
		if s.v == "%BASENAME%" {
			gName = gName[:len(gName)-len(filepath.Ext(gName))]
		}
		if gName != name {
			continue
		}
		if x.Path != "" && !filepath.IsAbs(x.Path) {
			x.Path = filepath.Clean(filepath.Join(filepath.Dir(s.GameListPath), x.Path))
		}
		if x.Image != "" && !filepath.IsAbs(x.Image) {
			x.Image = filepath.Clean(filepath.Join(filepath.Dir(s.GameListPath), x.Image))
		}
		if x.Thumb != "" && !filepath.IsAbs(x.Thumb) {
			x.Thumb = filepath.Clean(filepath.Join(filepath.Dir(s.GameListPath), x.Thumb))
		}
		return x, nil
	}
	return g, fmt.Errorf("%s: not found", game)
}

func (s *system) findGL() {
	paths := []string{
		fmt.Sprintf("%s/gamelist.xml", s.Path),
		fmt.Sprintf("%s/.emulationstation/gamelists/%s/gamelist.xml", s.home, s.Name),
		fmt.Sprintf("/etc/emulationstation/gamelists/%s/gamelist.xml", s.Name),
	}
	for _, p := range paths {
		if exists(p) {
			s.GameListPath = p
			break
		}
	}
}

func (s *system) Init(home string) error {
	s.home = home
	if strings.HasPrefix(s.Path, "~/") {
		s.Path = filepath.Join(s.home, strings.TrimPrefix(s.Path, "~/"))
	}
	err := s.buildRegexp()
	if err != nil {
		return err
	}
	s.findGL()
	return nil
}

func (s *system) buildRegexp() error {
	reStr := regexp.QuoteMeta(s.Command)
	var found bool
	vars := []string{"%ROM%", "%BASENAME%", "%ROM_RAW%"}
	for _, v := range vars {
		if s.Command == v {
			s.skip = true
			return nil
		}
		i := strings.Index(reStr, v)
		if i != -1 {
			if !found {
				found = true
				s.v = v
				reStr = strings.Replace(reStr, v, "(.+?)", 1)
			} else {
				reStr = strings.Replace(reStr, v, ".+?", 1)
			}
		}
	}
	re, err := regexp.Compile(fmt.Sprintf("\x00%s\x00", reStr))
	if err != nil {
		return err
	}
	s.re = re
	return nil
}

func (s *system) Match(cmd string) (string, error) {
	if s.skip {
		return "", nil
	}
	m := s.re.FindStringSubmatch(cmd)
	if m == nil {
		return "", nil
	}
	if s.v == "%ROM%" {
		var slash bool
		f := strings.Map(func(r rune) rune {
			if r == '\\' && !slash {
				slash = true
				return -1
			}
			slash = false
			return r
		}, m[1])
		return f, nil
	}
	return m[1], nil
}

type esSystems struct {
	XMLName xml.Name  `xml:"systemList"`
	Systems []*system `xml:"system"`
}

func getEmulators(hd string) ([]*system, error) {
	p := filepath.Join(hd, ".emulationstation", "es_systems.cfg")
	ap := "/etc/emulationstation/es_systems.cfg"
	if !exists(p) && !exists(ap) {
		return nil, fmt.Errorf("%s and %s not found.", p, ap)
	}
	if exists(ap) && !exists(p) {
		p = ap
	}
	d, err := ioutil.ReadFile(p)
	if err != nil {
		return nil, err
	}
	v := &esSystems{}
	err = xml.Unmarshal(d, &v)
	if err != nil {
		return nil, err
	}
	return v.Systems, nil
}
