package amazon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// ReturnPrep is what PrepareReturn managed to fill in before it stopped.
type ReturnPrep struct {
	OrderID  string   `json:"order_id"`
	StartURL string   `json:"start_url"`
	PageURL  string   `json:"page_url"`
	Item     string   `json:"item,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Comment  string   `json:"comment,omitempty"`
	Steps    []string `json:"steps"`
	Stopped  string   `json:"stopped_before"`
}

var returnLinkRe = []string{"/returns/", "/orc/returns", "returnitem", "spr/returns/cart", "/gp/orc/"}

// PrepareReturn opens the order in the (visible) browser, follows the
// "return or replace items" link, ticks the item, picks the reason that
// best matches, types the comment, and stops. It never presses the
// button that submits the return; the person does that, or closes the
// window. Every step it took is reported so it is clear what was and
// was not done.
func PrepareReturn(ctx context.Context, site Site, orderID, asin, reason, comment string) (ReturnPrep, error) {
	rp := ReturnPrep{OrderID: orderID, StartURL: site.OrderDetailsURL(orderID), Stopped: "the final submit button"}
	p, err := Load(ctx, rp.StartURL, 20*time.Second, "#orderDetails, #a-page")
	if err != nil {
		return rp, err
	}
	if IsSignInPage(p.URL) {
		return rp, ErrSignedOut
	}
	doc, err := p.Doc()
	if err != nil {
		return rp, err
	}
	link := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		for _, k := range returnLinkRe {
			if strings.Contains(strings.ToLower(href), k) && !strings.Contains(strings.ToLower(href), "help") {
				link = site.Absolute(href)
				return false
			}
		}
		return true
	})
	if link == "" {
		return rp, fmt.Errorf("no return link on the order page; the order may be outside its return window")
	}
	rp.Steps = append(rp.Steps, "opened the order page and found the return link")

	p, err = Load(ctx, link, 25*time.Second, "input[type=checkbox], input[type=radio], select, form")
	if err != nil {
		return rp, err
	}
	rp.PageURL = p.URL
	if IsSignInPage(p.URL) {
		return rp, ErrSignedOut
	}
	rp.Steps = append(rp.Steps, "opened the return flow at "+p.URL)

	// Tick the item: the box whose surrounding block mentions the ASIN
	// (or the only box when there is one).
	tickJS := `(() => {
  const asin = ` + jsString(strings.ToUpper(asin)) + `;
  const boxes = [...document.querySelectorAll('input[type=checkbox], input[type=radio]')].filter(b => b.offsetParent !== null);
  let pick = null;
  if (asin) {
    pick = boxes.find(b => { const blk = b.closest('li, .a-row, .a-box, div'); return blk && blk.outerHTML.includes(asin); });
  }
  if (!pick && boxes.length === 1) pick = boxes[0];
  if (!pick) return "";
  pick.click();
  const blk = pick.closest('li, .a-box, .a-row, div');
  return (blk ? blk.innerText : "").trim().split("\n")[0].slice(0, 120);
})()`
	var item string
	if err := chromedp.Run(ctx, chromedp.Evaluate(tickJS, &item)); err != nil {
		return rp, err
	}
	if item == "" {
		rp.Stopped = "choosing the item: no checkbox matched"
		return rp, nil
	}
	rp.Item = item
	rp.Steps = append(rp.Steps, "ticked the item: "+item)
	time.Sleep(1500 * time.Millisecond)

	// Pick the reason: a <select> whose option text matches, else a
	// radio whose label matches.
	reasonJS := `(() => {
  const want = ` + jsString(strings.ToLower(reason)) + `;
  for (const sel of document.querySelectorAll('select')) {
    if (sel.offsetParent === null) continue;
    for (const opt of sel.options) {
      if (opt.text.toLowerCase().includes(want)) { sel.value = opt.value; sel.dispatchEvent(new Event('change', {bubbles: true})); return opt.text.trim(); }
    }
  }
  for (const r of document.querySelectorAll('input[type=radio]')) {
    const lab = r.labels && r.labels[0] ? r.labels[0].innerText : (r.closest('label') || r.parentElement).innerText;
    if (lab && lab.toLowerCase().includes(want)) { r.click(); return lab.trim().slice(0, 120); }
  }
  return "";
})()`
	var picked string
	if err := chromedp.Run(ctx, chromedp.Evaluate(reasonJS, &picked)); err != nil {
		return rp, err
	}
	if picked == "" {
		rp.Stopped = "choosing the reason: nothing on the page matched " + fmt.Sprintf("%q", reason)
		return rp, nil
	}
	rp.Reason = picked
	rp.Steps = append(rp.Steps, "chose the reason: "+picked)
	time.Sleep(1500 * time.Millisecond)

	if comment != "" {
		commentJS := `(() => {
  const ta = [...document.querySelectorAll('textarea')].find(t => t.offsetParent !== null);
  if (!ta) return false;
  ta.focus(); ta.value = ` + jsString(comment) + `; ta.dispatchEvent(new Event('input', {bubbles: true})); return true;
})()`
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(commentJS, &ok)); err != nil {
			return rp, err
		}
		if ok {
			rp.Comment = comment
			rp.Steps = append(rp.Steps, "typed the comment")
		} else {
			rp.Steps = append(rp.Steps, "no comment box on this page; comment not entered")
		}
	}
	rp.Steps = append(rp.Steps, "stopped: the continue/submit button was not pressed")
	return rp, nil
}
