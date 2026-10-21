// Command demo records docs/demo.gif: the same agent flow driven through the
// real tool catalogue in Chrome (CDP) and Firefox (WebDriver BiDi), with an
// on-page caption showing each tool call.
//
//	go run ./scripts/demo -out docs/demo.gif -browsers chrome,firefox
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gwakko/agent-in-browser/internal/page"
	"github.com/gwakko/agent-in-browser/internal/session"
	"github.com/gwakko/agent-in-browser/internal/tools"
)

type frame struct {
	img   image.Image
	delay int // 1/100 s
}

const overlayJS = `
const id = '__aib_demo';
let bar = document.getElementById(id);
if (!bar) {
  bar = document.createElement('div'); bar.id = id; bar.setAttribute('aria-hidden', 'true');
  bar.style.cssText = 'position:fixed;pointer-events:none;left:0;right:0;bottom:0;z-index:2147483647;background:rgba(17,18,28,.94);color:#e8e8f0;' +
    'font:15px/1.4 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;padding:12px 20px;display:flex;gap:14px;align-items:center';
  document.documentElement.appendChild(bar);
}
bar.innerHTML = '';
const tag = document.createElement('span');
tag.textContent = ARGS.engine;
tag.style.cssText = 'background:' + ARGS.color + ';color:#fff;border-radius:6px;padding:2px 8px;font-weight:700;white-space:nowrap';
const call = document.createElement('span'); call.textContent = ARGS.call; call.style.cssText = 'color:#a5b4fc;white-space:nowrap';
const res = document.createElement('span'); res.textContent = ARGS.result ? '→ ' + ARGS.result : ''; res.style.cssText = 'color:#86efac;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
bar.append(tag, call, res);
document.querySelectorAll('.__aib_badge').forEach(b => b.remove());
if (ARGS.badges) {
  document.querySelectorAll('[data-aib-ref]').forEach(el => {
    const r = el.getBoundingClientRect(); if (!r.width) return;
    const b = document.createElement('div'); b.className = '__aib_badge'; b.setAttribute('aria-hidden', 'true');
    b.textContent = el.getAttribute('data-aib-ref');
    b.style.cssText = 'position:fixed;pointer-events:none;z-index:2147483646;left:' + (r.left - 4) + 'px;top:' + (r.top - 10) + 'px;background:#f43f5e;color:#fff;' +
      'font:700 11px ui-monospace,Menlo,monospace;padding:1px 5px;border-radius:5px;box-shadow:0 1px 3px rgba(0,0,0,.3)';
    document.documentElement.appendChild(b);
  });
}
if (ARGS.dot) {
  const d = document.createElement('div'); d.className = '__aib_badge';
  d.style.cssText = 'position:fixed;pointer-events:none;z-index:2147483646;width:26px;height:26px;border-radius:50%;border:3px solid #f43f5e;background:rgba(244,63,94,.25);' +
    'left:' + (ARGS.dot.x - 13) + 'px;top:' + (ARGS.dot.y - 13) + 'px';
  document.documentElement.appendChild(d);
}
return true;`

type rec struct {
	ctx    context.Context
	s      *session.Session
	engine string
	color  string
	frames []frame
}

func (r *rec) call(name string, args map[string]any) string {
	res := tools.Call(r.ctx, r.s, name, args)
	if res.IsError {
		log.Fatalf("%s: %s", name, res.Text())
	}
	return res.Text()
}

func (r *rec) shot(call, result string, delay int, badges bool, dot map[string]float64) {
	drv, tab, err := r.s.Tab(r.ctx, "")
	if err != nil {
		log.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"engine": r.engine, "color": r.color, "call": call, "result": result, "badges": badges, "dot": dot})
	js := "const ARGS = " + string(args) + ";\n" + overlayJS
	if err := r.s.Eval(r.ctx, drv, tab, page.Wrap(js), nil); err != nil {
		log.Fatal(err)
	}
	pngData, err := drv.Screenshot(r.ctx, tab)
	if err != nil {
		log.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		log.Fatal(err)
	}
	r.frames = append(r.frames, frame{img: img, delay: delay})
}

func center(r *rec, selector string) map[string]float64 {
	drv, tab, _ := r.s.Tab(r.ctx, "")
	var p struct{ X, Y float64 }
	if err := r.s.PageCall(r.ctx, drv, tab, &p, "point", map[string]any{"selector": selector}); err != nil {
		log.Fatal(err)
	}
	return map[string]float64{"x": p.X, "y": p.Y}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func record(ctx context.Context, browserName, url string, w, h int) []frame {
	engines := map[string][2]string{
		"chrome":  {"Chrome · CDP", "#2563eb"},
		"edge":    {"Edge · CDP", "#0e7490"},
		"brave":   {"Brave · CDP", "#ea580c"},
		"firefox": {"Firefox · WebDriver BiDi", "#c2410c"},
	}
	e, ok := engines[browserName]
	if !ok {
		e = [2]string{browserName, "#475569"}
	}
	s := session.New(session.Config{Browser: browserName, Headless: true, Ephemeral: true, Width: w, Height: h})
	defer s.Close()
	r := &rec{ctx: ctx, s: s, engine: e[0], color: e[1]}

	r.call("resize", map[string]any{"width": w, "height": h})
	out := r.call("navigate", map[string]any{"url": url})
	r.shot(`navigate("acme.cloud/signup")`, strings.TrimPrefix(firstLine(strings.SplitN(out, "\n", 3)[1]), "Title: "), 160, false, nil)

	snap := r.call("snapshot", nil)
	n := 0
	for _, l := range strings.Split(snap, "\n") {
		if strings.Contains(l, "[ref=") {
			n++
		}
	}
	r.shot("snapshot()", fmt.Sprintf("%d interactive elements, each with a ref", n), 220, true, nil)

	ref := "e?"
	if m := regexp.MustCompile(`"Work email" \[ref=(e\d+)\]`).FindStringSubmatch(snap); m != nil {
		ref = m[1]
	}
	email := "ada@acme.dev"
	r.call("type", map[string]any{"selector": "#email", "text": ""})
	for i := 3; i <= len(email); i += 3 {
		r.call("type", map[string]any{"selector": "#email", "text": email[:i]})
		r.shot(fmt.Sprintf(`type(ref=%q, text=%q)`, ref, email), "", 28, false, nil)
	}
	r.shot(fmt.Sprintf(`type(ref=%q, text=%q)`, ref, email), "typed 12 chars", 90, false, nil)

	r.call("fill_form", map[string]any{"fields": []any{
		map[string]any{"selector": "#ws", "value": "acme-labs"},
		map[string]any{"selector": "#plan", "value": "Pro"},
		map[string]any{"selector": "#tos", "value": true},
	}})
	r.shot(`fill_form([workspace, plan="Pro", terms=true])`, "set 3 fields", 170, false, nil)

	dot := center(r, "button[type=submit]")
	r.shot(`click(match="Create workspace")`, "", 90, false, dot)
	r.call("click", map[string]any{"match": "Create workspace"})
	r.call("wait_for", map[string]any{"text": "Welcome"})
	r.shot(`wait_for(text="Welcome")`, "Welcome to acme-labs!", 260, false, nil)
	return r.frames
}

// palette builds a per-frame palette from the most frequent colours.
func palette(img image.Image) color.Palette {
	counts := map[uint32]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			r, g, bl, _ := img.At(x, y).RGBA()
			key := (r>>11)<<10 | (g>>11)<<5 | (bl >> 11) // 5 bits per channel
			counts[key]++
		}
	}
	type kv struct {
		k uint32
		n int
	}
	list := make([]kv, 0, len(counts))
	for k, n := range counts {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	p := color.Palette{color.Black, color.White}
	for _, e := range list {
		if len(p) == 256 {
			break
		}
		r := uint8((e.k>>10)&31) << 3
		g := uint8((e.k>>5)&31) << 3
		bl := uint8(e.k&31) << 3
		p = append(p, color.RGBA{r | r>>5, g | g>>5, bl | bl>>5, 255})
	}
	return p
}

func main() {
	out := flag.String("out", "docs/demo.gif", "output GIF")
	browsers := flag.String("browsers", "chrome,firefox", "browsers to record, in order")
	w := flag.Int("width", 1000, "viewport width")
	h := flag.Int("height", 600, "viewport height")
	flag.Parse()

	abs, _ := filepath.Abs("examples/fixtures/demo.html")
	url := "file://" + filepath.ToSlash(abs)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var frames []frame
	for _, b := range strings.Split(*browsers, ",") {
		log.Printf("recording %s", b)
		frames = append(frames, record(ctx, strings.TrimSpace(b), url, *w, *h)...)
	}

	anim := &gif.GIF{LoopCount: 0}
	for _, f := range frames {
		b := f.img.Bounds()
		pm := image.NewPaletted(image.Rect(0, 0, b.Dx(), b.Dy()), palette(f.img))
		draw.Draw(pm, pm.Rect, f.img, b.Min, draw.Src)
		anim.Image = append(anim.Image, pm)
		anim.Delay = append(anim.Delay, f.delay)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	fh, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer fh.Close()
	if err := gif.EncodeAll(fh, anim); err != nil {
		log.Fatal(err)
	}
	st, _ := fh.Stat()
	log.Printf("wrote %s: %d frames, %.1f MB", *out, len(frames), float64(st.Size())/1e6)
}
