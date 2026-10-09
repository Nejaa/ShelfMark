package library

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Ordering groups series, orders their volumes numerically, then compares names.
// Each caller owns its collator; collators are not safe for concurrent use.
type Ordering struct{ names *collate.Collator }

func NewOrdering() *Ordering {
	return &Ordering{names: collate.New(language.Und, collate.Loose, collate.Numeric)}
}

// Compare uses bibliographic metadata, with filename and path as stable ties.
func (o *Ordering) Compare(a, b map[string]any, aName, bName, aPath, bPath string) int {
	as, bs := strings.TrimSpace(Text(a["series"])), strings.TrimSpace(Text(b["series"]))
	if as == "" && bs != "" {
		return 1
	}
	if as != "" && bs == "" {
		return -1
	}
	if as != "" {
		if n := o.names.CompareString(as, bs); n != 0 {
			return n
		}
		if n := o.volumes(Text(a["series_index"]), Text(b["series_index"])); n != 0 {
			return n
		}
	}
	for _, pair := range [][2]string{{DisplayTitle(a, aName), DisplayTitle(b, bName)}, {aName, bName}, {aPath, bPath}} {
		if n := o.names.CompareString(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	return strings.Compare(aPath, bPath)
}

func (o *Ordering) volumes(a, b string) int {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" && b != "" {
		return 1
	}
	if a != "" && b == "" {
		return -1
	}
	an, ae := strconv.ParseFloat(strings.ReplaceAll(a, ",", "."), 64)
	bn, be := strconv.ParseFloat(strings.ReplaceAll(b, ",", "."), 64)
	if ae == nil && be == nil && !math.IsNaN(an) && !math.IsNaN(bn) && !math.IsInf(an, 0) && !math.IsInf(bn, 0) {
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
		return 0
	}
	return o.names.CompareString(a, b)
}
