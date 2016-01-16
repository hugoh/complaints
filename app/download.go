package complaints

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"
	
	"appengine"

	"github.com/skypies/date"
	
	"github.com/skypies/complaints/complaintdb"
	"github.com/skypies/complaints/complaintdb/types"
	"github.com/skypies/complaints/sessions"
)

func init() {
	http.HandleFunc("/download-complaints", downloadHandler)
	http.HandleFunc("/personal-report", personalReportHandler)
	http.HandleFunc("/personal-report/results", personalReportHandler) // Legacy
	http.HandleFunc("/summary-report", summaryReportHandler)
	//http.HandleFunc("/backfill", backfillHandler)
	http.HandleFunc("/month", monthHandler)
}

// {{{ keysByIntValDesc

// Yay, sorting things is so easy in go
func keysByIntValDesc(m map[string]int) []string {
	// Invert the map
	inv := map[int][]string{}
	for k,v := range m { inv[v] = append(inv[v], k) }

	// List the unique vals
	vals := []int{}
	for k,_ := range inv { vals = append(vals, k) }

	// Sort the vals
	sort.Sort(sort.Reverse(sort.IntSlice(vals)))

	// Now list the keys corresponding to each val
	keys := []string{}
	for _,val := range vals {
		for _,key := range inv[val] {
			keys = append(keys, key)
		}
	}

	return keys
}

// }}}
// {{{ keysByKeyAsc

func keysByKeyAsc(m map[string]int) []string {
	// List the unique vals
	keys := []string{}
	for k,_ := range m { keys = append(keys, k) }

	// Sort the vals
	sort.Sort(sort.StringSlice(keys))

	return keys
}

// }}}

// {{{ monthHandler

// http://stop.jetnoise.net/month?year=2015&month=9&day=1&num=10
// http://stop.jetnoise.net/month?year=2015&month=9&day=11&num=10
// http://stop.jetnoise.net/month?year=2015&month=9&day=21&num=10

// http://stop.jetnoise.net/month?year=2015&month=10&day=1&num=10
// http://stop.jetnoise.net/month?year=2015&month=10&day=11&num=10
// http://stop.jetnoise.net/month?year=2015&month=10&day=21&num=11  <-- 31st day

func monthHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.Timeout(appengine.NewContext(r), 180*time.Second)

	year,err := strconv.ParseInt(r.FormValue("year"), 10, 64)
	if err != nil {
		http.Error(w, "need arg 'year' (2015)", http.StatusInternalServerError)
		return
	}
	month,err := strconv.ParseInt(r.FormValue("month"), 10, 64)
	if err != nil {
		http.Error(w, "need arg 'month' (1-12)", http.StatusInternalServerError)
		return
	}
	day,err := strconv.ParseInt(r.FormValue("day"), 10, 64)
	if err != nil {
		http.Error(w, "need arg 'day' (1-31)", http.StatusInternalServerError)
		return
	}
	num,err := strconv.ParseInt(r.FormValue("num"), 10, 64)
	if err != nil {
		http.Error(w, "need arg 'num' (31 - 'day')", http.StatusInternalServerError)
		return
	}
	now := date.NowInPdt()
	firstOfMonth := time.Date(int(year), time.Month(month), 1, 0,0,0,0, now.Location())
	s := firstOfMonth.AddDate(0,0,int(day-1))
	e := s.AddDate(0,0,int(num)).Add(-1 * time.Second)

	ctx.Infof("Yow: START : %s", s)
	ctx.Infof("Yow: END   : %s", e)

	cdb := complaintdb.ComplaintDB{C: ctx}

	filename := s.Format("complaints-20060102") + e.Format("-20060102.csv")
	w.Header().Set("Content-Type", "application/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	cols := []string{
		"CallerCode", "Name", "Address", "Zip", "Email", "HomeLat", "HomeLong", 
		"UnixEpoch", "Date", "Time(PDT)",
		"Notes", "Flightnumber", "ActivityDisturbed", "AutoSubmit",
	}
	csvWriter := csv.NewWriter(w)
	csvWriter.Write(cols)
	
	iter := cdb.NewIter(cdb.QueryInSpan(s,e))
	for {
		c := iter.Next();
		if c == nil { break }

		r := []string{
			c.Profile.CallerCode, c.Profile.FullName, c.Profile.Address,
			c.Profile.StructuredAddress.Zip, c.Profile.EmailAddress,
			fmt.Sprintf("%.4f",c.Profile.Lat), fmt.Sprintf("%.4f",c.Profile.Long),
			fmt.Sprintf("%d", c.Timestamp.UTC().Unix()),
			c.Timestamp.Format("2006/01/02"),
			c.Timestamp.Format("15:04:05"),
			c.Description, c.AircraftOverhead.FlightNumber, c.Activity,
			fmt.Sprintf("%v",c.Profile.CcSfo),
		}
		
		if err := csvWriter.Write(r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	csvWriter.Flush()
}

// }}}
// {{{ outputCSV

func outputCSV(w *csv.Writer, p types.ComplainerProfile, c types.Complaint) {
	zip := regexp.MustCompile("^.*(\\d{5}(-\\d{4})?).*$").ReplaceAllString(p.Address, "$1")

	r := []string{
		p.CallerCode, p.FullName, p.Address, zip, p.EmailAddress,
		fmt.Sprintf("%.4f",p.Lat), fmt.Sprintf("%.4f",p.Long),
		fmt.Sprintf("%d", c.Timestamp.UTC().Unix()),
		c.Timestamp.Format("2006/01/02"),
		c.Timestamp.Format("15:04:05"),
		c.Description, c.AircraftOverhead.FlightNumber, c.Activity,
	}

	if err := w.Write(r); err != nil {
		// ?
	}
}

// }}}
// {{{ backfillHandler

func backfillHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	cdb := complaintdb.ComplaintDB{C: c}

	profiles, err := cdb.GetAllProfiles()
	if err != nil { return }

	filename := date.NowInPdt().Format("complaints-backfill.csv")
	w.Header().Set("Content-Type", "application/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	cols := []string{
		"CallerCode", "Name", "Address", "Zip", "Email", "HomeLat", "HomeLong", 
		"UnixEpoch", "Date", "Time(PDT)", "Notes", "ActivityDisturbed", "Flightnumber", "Notes",
	}
	
	csvWriter := csv.NewWriter(w)
	csvWriter.Write(cols)
	
	// Walk backwards in time, until there is no data
	ts,te := date.WindowForYesterday()  // end is the final day we count for; yesterday
	for {
		n := 0
		// c.Infof ("---------- Looking at ts=%s", ts)
		for _,p := range profiles {
			if p.CcSfo == false {
				// c.Infof ("---{ SKIP %s }---", p.EmailAddress)
				continue
			}
			// c.Infof ("---{ %s }---", p.EmailAddress)
			if complaints,err := cdb.GetComplaintsInSpanByEmailAddress(p.EmailAddress,ts,te); err!=nil {
				c.Errorf("Arse;ts=%s, err=%v", ts, err)
			} else {
				n += len(complaints)
				for _,complaint := range complaints {
					outputCSV(csvWriter, p, complaint)
				}
			}
		}
		if (n == 0) { break }
		ts = ts.AddDate(0,0,-1)
		te = te.AddDate(0,0,-1)
	}
	//c.Infof("All done!")
	csvWriter.Flush()
}

// }}}

// {{{ downloadHandler

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	session := sessions.Get(r)
	if session.Values["email"] == nil {
		http.Error(w, "session was empty; no cookie ? is this browser in privacy mode ?",
			http.StatusInternalServerError)
		return
	}

	cdb := complaintdb.ComplaintDB{C: c}
	cap, err := cdb.GetAllByEmailAddress(session.Values["email"].(string), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = cap

	filename := date.NowInPdt().Format("complaints-20060102.csv")
	w.Header().Set("Content-Type", "application/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	cols := []string{
		"Date", "Time(PDT)", "Notes", "Speedbrakes", "Loudness", "Activity",
		"Flightnumber", "Origin", "Destination", "Speed(Knots)", "Altitude(Feet)",
		"Lat", "Long", "Registration", "Callsign",
		"VerticalSpeed(FeetPerMin)", "Dist2(km)", "Dist3(km)",
	}
	
	csvWriter := csv.NewWriter(w)
	csvWriter.Write(cols)

	for _,c := range cap.Complaints {
		a := c.AircraftOverhead
		speedbrakes := ""
		if c.HeardSpeedbreaks { speedbrakes = "y" }
		r := []string{
			c.Timestamp.Format("2006/01/02"),
			c.Timestamp.Format("15:04:05"),
			c.Description, speedbrakes, fmt.Sprintf("%d", c.Loudness), c.Activity,
			a.FlightNumber, a.Origin, a.Destination,
			fmt.Sprintf("%.0f",a.Speed), fmt.Sprintf("%.0f",a.Altitude),
			fmt.Sprintf("%.5f", a.Lat), fmt.Sprintf("%.5f", a.Long),
			a.Registration, a.Callsign, fmt.Sprintf("%.0f",a.VerticalSpeed),
			fmt.Sprintf("%.1f", c.Dist2KM), fmt.Sprintf("%.1f", c.Dist3KM),
		}

		if err := csvWriter.Write(r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	csvWriter.Flush()
}

// }}}
// {{{ personalReportHandler

func personalReportHandler(w http.ResponseWriter, r *http.Request) {
	session := sessions.Get(r)
	if session.Values["email"] == nil {
		http.Error(w, "session was empty; no cookie ?", http.StatusInternalServerError)
		return
	}
	email := session.Values["email"].(string)

	if r.FormValue("date") == "" {
		var params = map[string]interface{}{
			"Yesterday": date.NowInPdt().AddDate(0,0,-1),
		}
		if err := templates.ExecuteTemplate(w, "report-personal-form", params); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}


	start,end,_ := FormValueDateRange(r)

	ctx := appengine.Timeout(appengine.NewContext(r), 60*time.Second)
	cdb := complaintdb.ComplaintDB{C: ctx}

	w.Header().Set("Content-Type", "text/plain")
	// w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", "sc.txt"))
	fmt.Fprintf(w, "Personal disturbances report for <%s>:\n From [%s]\n To   [%s]\n",
		email, start, end)

	complaintStrings := []string{}
	var countsByHour [24]int
	countsByDate := map[string]int{}
	countsByAirline := map[string]int{}
	
	iter := cdb.NewIter(cdb.QueryInSpanByEmailAddress(start,end,email))
	n := 0
	for {
		c := iter.Next();
		if c == nil { break }

		str := fmt.Sprintf("Time: %s, Loudness:%d, Speedbrakes:%v, Flight:%6.6s, Notes:%s",
			c.Timestamp.Format("2006.01.02 15:04:05"), c.Loudness, c.HeardSpeedbreaks,
			c.AircraftOverhead.FlightNumber, c.Description)
		
		n++
		complaintStrings = append(complaintStrings, str)
		
		countsByHour[c.Timestamp.Hour()]++
		countsByDate[c.Timestamp.Format("2006.01.02")]++
		if airline := c.AircraftOverhead.IATAAirlineCode(); airline != "" {
			countsByAirline[airline]++
		}
	}

	fmt.Fprintf(w, "\nTotal number of disturbance reports, over %d days:  %d\n",
		len(countsByDate), n)

	fmt.Fprintf(w, "\nDisturbance reports, counted by Airline (where known):\n")
	for _,k := range keysByIntValDesc(countsByAirline) {
		fmt.Fprintf(w, " %s: % 4d\n", k, countsByAirline[k])
	}
	fmt.Fprintf(w, "\nDisturbance reports, counted by date:\n")
	for k,v := range countsByDate {
		fmt.Fprintf(w, " %s: % 4d\n", k, v)
	}
	fmt.Fprintf(w, "\nDisturbance reports, counted by hour of day (across all dates):\n")
	for i,n := range countsByHour {
		fmt.Fprintf(w, " %02d: % 4d\n", i, n)
	}
	fmt.Fprintf(w, "\nFull dump of all disturbance reports:\n\n")
	for _,s := range complaintStrings {
		fmt.Fprint(w, s+"\n")
	}
}

// }}}
// {{{ summaryReportHandler

func summaryReportHandler(w http.ResponseWriter, r *http.Request) {
	session := sessions.Get(r)
	if session.Values["email"] == nil {
		http.Error(w, "session was empty; no cookie ?", http.StatusInternalServerError)
		return
	}

	if r.FormValue("date") == "" {
		var params = map[string]interface{}{
			"Yesterday": date.NowInPdt().AddDate(0,0,-1),
		}
		if err := templates.ExecuteTemplate(w, "report-summary-form", params); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}


	start,end,_ := FormValueDateRange(r)

	ctx := appengine.Timeout(appengine.NewContext(r), 60*time.Second)
	cdb := complaintdb.ComplaintDB{C: ctx}

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "Summary of disturbance reports:\n From [%s]\n To   [%s]\n", start, end)

	var countsByHour [24]int
	countsByDate := map[string]int{}
	countsByAirline := map[string]int{}
	countsByEquip := map[string]int{}
	countsByCity := map[string]int{}

	uniquesAll := map[string]int{}
	uniquesByDate := map[string]map[string]int{}
	uniquesByCity := map[string]map[string]int{}
	
	iter := cdb.NewIter(cdb.QueryInSpan(start,end))
	n := 0
	for {
		c,err := iter.NextWithErr();
		if err != nil {
			http.Error(w, fmt.Sprintf("iterator failed: %v", err), http.StatusInternalServerError)
			return
		} else if c == nil {
			break
		}

		n++		
		uniquesAll[c.Profile.EmailAddress]++
		countsByHour[c.Timestamp.Hour()]++

		d := c.Timestamp.Format("2006.01.02")
		countsByDate[d]++
		if uniquesByDate[d] == nil { uniquesByDate[d] = map[string]int{} }
		uniquesByDate[d][c.Profile.EmailAddress]++

		if airline := c.AircraftOverhead.IATAAirlineCode(); airline != "" {
			countsByAirline[airline]++
		}

		if city := c.Profile.GetStructuredAddress().City; city != "" {
			countsByCity[city]++
			if uniquesByCity[city] == nil { uniquesByCity[city] = map[string]int{} }
			uniquesByCity[city][c.Profile.EmailAddress]++
		}
		if equip := c.AircraftOverhead.EquipType; equip != "" {
			countsByEquip[equip]++
		}
	}

	fmt.Fprintf(w, "\nTotals:\n Days                : %d\n"+
		" Disturbance reports : %d\n People reporting    : %d\n",
		len(countsByDate), n, len(uniquesAll))

	fmt.Fprintf(w, "\nDisturbance reports, counted by City (where known):\n")
	for _,k := range keysByIntValDesc(countsByCity) {
		fmt.Fprintf(w, " %-40.40s: % 5d (% 3d people reporting)\n", k, countsByCity[k],
			len(uniquesByCity[k]))
	}
	fmt.Fprintf(w, "\nDisturbance reports, counted by date:\n")
	for _,k := range keysByKeyAsc(countsByDate) {
		fmt.Fprintf(w, " %s: % 5d (% 3d people reporting)\n", k, countsByDate[k], len(uniquesByDate[k]))
	}

	fmt.Fprintf(w, "\nDisturbance reports, counted by aircraft equipment type (where known):\n")
	for _,k := range keysByIntValDesc(countsByEquip) {
		if countsByEquip[k] < 5 { break }
		fmt.Fprintf(w, " %-40.40s: % 5d\n", k, countsByEquip[k])
	}

	fmt.Fprintf(w, "\nDisturbance reports, counted by Airline (where known):\n")
	for _,k := range keysByIntValDesc(countsByAirline) {
		if countsByAirline[k] < 5 || len(k) > 2 { continue }
		fmt.Fprintf(w, " %s: % 6d\n", k, countsByAirline[k])
	}

	fmt.Fprintf(w, "\nDisturbance reports, counted by hour of day (across all dates):\n")
	for i,n := range countsByHour {
		fmt.Fprintf(w, " %02d: % 5d\n", i, n)
	}
}

// }}}

// {{{ -------------------------={ E N D }=----------------------------------

// Local variables:
// folded-file: t
// end:

// }}}
