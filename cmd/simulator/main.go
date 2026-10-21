// Command simulator drives a small fleet of vehicles around Indonesia and
// reports their positions to the API, so the live-tracking map has a real data
// source instead of markers moving on their own.
//
//	go run ./cmd/simulator -count 5 -interval 2s
//
// It creates the missing vehicles once and then reports one position per
// vehicle per interval until it is stopped with Ctrl+C.
//
// Every tick the simulator asks the API which vehicles have an active
// delivery route (GET /api/routes). A unit without a route wanders the way a
// GPS feed of a road vehicle looks; a unit with a route drives the planned
// line step by step, from the first point to the last one, and the server
// closes the route when it arrives.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// statusError carries the HTTP status of a failed call, so the caller can tell
// "this unit is gone" (404) from a real failure.
type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string { return e.err.Error() }

func (e *statusError) Unwrap() error { return e.err }

// startPoints are real Indonesian cities, so the simulated fleet appears where
// a real fleet would: Jakarta, Bandung, Semarang, Surabaya, Medan, Makassar.
var startPoints = [][2]float64{
	{-6.1754, 106.8272},
	{-6.9175, 107.6191},
	{-6.9667, 110.4167},
	{-7.2575, 112.7521},
	{3.5952, 98.6722},
	{-5.1477, 119.4327},
}

// bounds keep the fleet inside Indonesia.
const (
	minLat, maxLat = -11.0, 6.0
	minLng, maxLng = 95.0, 141.0
)

type unit struct {
	id   int64
	code string
	name string
	lat  float64
	lng  float64
	dir  float64
	// routeID/routePos track the delivery route the unit is following, so a
	// route set from the browser is actually driven instead of ignored.
	routeID  int64
	routePos float64
	// arrived remembers that the unit reached the end of its route, so the
	// simulator can make sure the route really is closed in the database.
	arrived bool
}

type apiVehicle struct {
	ID   int64    `json:"id"`
	Code string   `json:"code"`
	Name string   `json:"name"`
	Lat  *float64 `json:"lat"`
	Lng  *float64 `json:"lng"`
}

type listResponse struct {
	Data struct {
		Vehicles []apiVehicle `json:"vehicles"`
	} `json:"data"`
}

// apiRoute is the part of GET /api/routes the simulator needs: which unit
// follows which line.
type apiRoute struct {
	ID        int64 `json:"id"`
	VehicleID int64 `json:"vehicle_id"`
	Geometry  struct {
		Type        string       `json:"type"`
		Coordinates [][2]float64 `json:"coordinates"`
	} `json:"geometry"`
}

type routeListResponse struct {
	Data struct {
		Routes []apiRoute `json:"routes"`
	} `json:"data"`
}

// pathPlan is one route prepared for interpolation: points in lat/lng plus the
// cumulative distance at each point, so a distance along the line maps to a
// position in O(segments).
type pathPlan struct {
	id    int64
	pts   [][2]float64
	cum   []float64
	total float64
}

func main() {
	api := flag.String("api", "http://localhost:8080", "base URL of the API")
	count := flag.Int("count", 5, "number of vehicles to keep on the map")
	interval := flag.Duration("interval", 2*time.Second, "delay between position reports")
	seed := flag.Int64("seed", 1, "random seed, so a run can be repeated")
	email := flag.String("email", "", "account email for API login")
	password := flag.String("password", "", "account password for API login")
	flag.Parse()

	if *count < 1 || *count > len(startPoints)*4 {
		log.Fatalf("count must be between 1 and %d", len(startPoints)*4)
	}
	if *interval < 200*time.Millisecond {
		log.Fatalf("interval must be at least 200ms")
	}
	if *email == "" || *password == "" {
		log.Fatalf("email and password are required (an admin creates the account from the Users tab)")
	}

	client := &http.Client{Timeout: 10 * time.Second}

	// Login to get a bearer token.
	token, err := login(client, *api, *email, *password)
	if err != nil {
		log.Fatalf("login: %v", err)
	}
	log.Printf("authenticated as %s", *email)

	fleet, err := ensureFleet(client, *api, *count, token)
	if err != nil {
		log.Fatalf("prepare fleet: %v", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	rng := rand.New(rand.NewSource(*seed))
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	log.Printf("simulating %d vehicle(s) against %s every %s", len(fleet), *api, *interval)

	for {
		select {
		case <-stop:
			log.Println("simulator stopped")
			return
		case <-ticker.C:
			fleet, err = report(client, *api, fleet, rng, *interval, token)
			if err != nil {
				log.Printf("report: %v", err)
			}
		}
	}
}

// ensureFleet fetches the tracked vehicles and creates the missing ones until
// the fleet is at least as large as requested.
func ensureFleet(client *http.Client, api string, count int, token string) ([]*unit, error) {
	existing, err := listVehicles(client, api, token)
	if err != nil {
		return nil, err
	}

	fleet := make([]*unit, 0, count)
	for _, v := range existing {
		if len(fleet) >= count {
			break
		}
		fleet = append(fleet, newUnit(v, len(fleet)))
	}

	for len(fleet) < count {
		index := len(fleet)
		code := fmt.Sprintf("SIM-%02d", index+1)
		created, err := createVehicle(client, api, code, fmt.Sprintf("Simulasi %02d", index+1), token)
		if err != nil {
			return nil, err
		}
		fleet = append(fleet, newUnit(created, index))
	}

	return fleet, nil
}

// newUnit places a vehicle at its last known position, or at a start point when
// it has never reported one.
func newUnit(v apiVehicle, index int) *unit {
	start := startPoints[index%len(startPoints)]
	u := &unit{id: v.ID, code: v.Code, name: v.Name, lat: start[0], lng: start[1], dir: 0.5}
	if v.Lat != nil {
		u.lat = *v.Lat
	}
	if v.Lng != nil {
		u.lng = *v.Lng
	}

	return u
}

// report advances every vehicle by one interval and posts the new position.
// A unit that was deleted in the app is dropped from the fleet instead of
// failing every tick, so the check that rejects its position is not wasted.
func report(client *http.Client, api string, fleet []*unit, rng *rand.Rand, interval time.Duration, token string) ([]*unit, error) {
	plans, err := fetchRoutes(client, api, token)
	if err != nil {
		// A route feed that is down must not stop the position feed.
		log.Printf("routes: %v", err)
	}

	kept := fleet[:0]
	progress := make([]string, 0, len(fleet))
	for _, u := range fleet {
		speed := 0.0
		tag := ""
		status := "moving"
		if plan := plans[u.id]; plan != nil {
			speed = u.follow(plan, interval)
			tag = fmt.Sprintf(" rute#%d", plan.id)
			if speed == 0 {
				// The unit reached the end of the line: it parks there until
				// the server closes the delivery.
				status = "idle"
			}
		} else {
			// No route (any more): the unit wanders again. If it had just
			// finished a delivery, the route is closed in the database, so the
			// next tick the map shows no line for this unit.
			if u.arrived {
				u.arrived = false
				if err := clearRoute(client, api, u.id, token); err != nil {
					log.Printf("%s: could not close the finished route: %v", u.code, err)
				} else {
					tag = " rute selesai"
				}
			}
			u.routeID = 0
			speed = u.step(rng, interval)
		}

		body := map[string]any{
			"lat":       u.lat,
			"lng":       u.lng,
			"speed_kmh": math.Round(speed*10) / 10,
			"status":    status,
			// The direction the unit faces: a compass bearing in degrees,
			// which is what the map turns the marker by.
			"heading": math.Round(toDegrees(u.dir)*10) / 10,
		}
		if err := postJSON(client, fmt.Sprintf("%s/api/vehicles/%d/position", api, u.id), body, nil, token); err != nil {
			var se *statusError
			if errors.As(err, &se) && se.status == http.StatusNotFound {
				log.Printf("%s: no longer in the fleet, dropping it", u.code)
				continue
			}
			return kept, fmt.Errorf("%s: %w", u.code, err)
		}
		kept = append(kept, u)
		progress = append(progress, fmt.Sprintf("%s %.4f,%.4f %.0fkm/h%s", u.code, u.lat, u.lng, speed, tag))
	}

	log.Printf("%s", joinLines(progress))

	return kept, nil
}

// clearRoute removes the active route of a vehicle. The server closes a
// route on its own when the unit arrives, but the simulator does not rely on
// it: a route left active would keep the line on the map for a delivery that
// is already over.
func clearRoute(client *http.Client, api string, vehicleID int64, token string) error {
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/vehicles/%d/route", api, vehicleID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}

	return fmt.Errorf("DELETE /api/vehicles/%d/route returned %s (%s)", vehicleID, resp.Status, bytes.TrimSpace(raw))
}

// fetchRoutes returns the active delivery routes keyed by vehicle id.
func fetchRoutes(client *http.Client, api, token string) (map[int64]*pathPlan, error) {
	req, err := http.NewRequest(http.MethodGet, api+"/api/routes", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/routes returned %s", resp.Status)
	}

	var out routeListResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode routes: %w", err)
	}

	plans := make(map[int64]*pathPlan, len(out.Data.Routes))
	for _, r := range out.Data.Routes {
		if plan := newPathPlan(r); plan != nil {
			plans[r.VehicleID] = plan
		}
	}

	return plans, nil
}

// newPathPlan prepares a GeoJSON LineString for interpolation.
func newPathPlan(r apiRoute) *pathPlan {
	if len(r.Geometry.Coordinates) < 2 {
		return nil
	}

	plan := &pathPlan{
		id:  r.ID,
		pts: make([][2]float64, 0, len(r.Geometry.Coordinates)),
		cum: make([]float64, 0, len(r.Geometry.Coordinates)),
	}
	for _, pair := range r.Geometry.Coordinates {
		plan.pts = append(plan.pts, [2]float64{pair[1], pair[0]}) // GeoJSON is lng,lat
	}

	// A line that repeats a point has no length to drive: the unit would sit
	// on the spot forever. Such a line is dropped, and the unit wanders as
	// if it had no route at all.
	plan.pts = dropRepeated(plan.pts)
	if len(plan.pts) < 2 {
		return nil
	}

	plan.cum = append(plan.cum, 0)
	for i := 1; i < len(plan.pts); i++ {
		plan.total += haversineMeters(plan.pts[i-1][0], plan.pts[i-1][1], plan.pts[i][0], plan.pts[i][1])
		plan.cum = append(plan.cum, plan.total)
	}
	if plan.total <= 0 {
		return nil
	}

	return plan
}

// dropRepeated removes the points that repeat their predecessor.
func dropRepeated(pts [][2]float64) [][2]float64 {
	out := make([][2]float64, 0, len(pts))
	for _, p := range pts {
		if len(out) > 0 {
			last := out[len(out)-1]
			if math.Abs(last[0]-p[0]) < 1e-7 && math.Abs(last[1]-p[1]) < 1e-7 {
				continue
			}
		}
		out = append(out, p)
	}

	return out
}

// follow drives a unit along its route and returns the speed it reports.
//
// A new route always starts at its origin. Projecting the truck's current spot
// onto the line looks tempting, but a unit parked in another province lands on
// whichever endpoint is nearest — usually the destination — so the delivery
// would "finish" without driving anywhere. A delivery plan begins at the
// pickup, so that is where the unit is placed when the plan arrives.
//
// The speed is 0 on the last step: the unit has arrived, and the server
// closes the route when it reports that position.
func (u *unit) follow(plan *pathPlan, interval time.Duration) float64 {
	if u.routeID != plan.id {
		u.routeID = plan.id
		u.routePos = 0
		u.arrived = false
	}

	speed := 30 + float64(u.id%4)*6 // 30-48 km/h, stable per unit
	u.routePos += speed / 3.6 * interval.Seconds()
	if u.routePos >= plan.total {
		u.routePos = plan.total
		speed = 0 // arrived; the API closes the route on the next position
		u.arrived = true
	}

	u.lat, u.lng = pointAt(plan, u.routePos)
	u.dir = heading(plan, u.routePos)

	return speed
}

// pointAt returns the coordinate at a distance along the line.
func pointAt(plan *pathPlan, distance float64) (float64, float64) {
	if distance <= 0 {
		return plan.pts[0][0], plan.pts[0][1]
	}
	if distance >= plan.total {
		last := plan.pts[len(plan.pts)-1]
		return last[0], last[1]
	}

	for i := 0; i+1 < len(plan.pts); i++ {
		if distance <= plan.cum[i+1] {
			seg := plan.cum[i+1] - plan.cum[i]
			t := 0.0
			if seg > 0 {
				t = (distance - plan.cum[i]) / seg
			}
			lat := plan.pts[i][0] + t*(plan.pts[i+1][0]-plan.pts[i][0])
			lng := plan.pts[i][1] + t*(plan.pts[i+1][1]-plan.pts[i][1])
			return lat, lng
		}
	}

	last := plan.pts[len(plan.pts)-1]
	return last[0], last[1]
}

// heading is the compass direction the unit is facing, in radians, used by the
// free-roam step when the route ends.
func heading(plan *pathPlan, distance float64) float64 {
	lat, lng := pointAt(plan, distance)
	aheadLat, aheadLng := pointAt(plan, math.Min(distance+10, plan.total))
	dLat := aheadLat - lat
	dLng := (aheadLng - lng) * math.Cos(lat*math.Pi/180)
	if dLat == 0 && dLng == 0 {
		return 0
	}

	return math.Atan2(dLat, dLng)
}

// haversineMeters is the great-circle distance between two coordinates.
func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6_371_000.0

	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)

	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}

// toDegrees turns a compass direction in radians into degrees (0 = north,
// 90 = east), the unit the API and the map use.
func toDegrees(rad float64) float64 {
	deg := rad * 180 / math.Pi
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	return deg
}

func joinLines(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " | "
		}
		out += p
	}

	return out
}

// step moves one unit for one interval: a slow random turn and a fresh speed
// per tick, the way a GPS feed of a road vehicle looks.
func (u *unit) step(rng *rand.Rand, interval time.Duration) float64 {
	u.dir += (rng.Float64() - 0.5) * 0.5 // up to ~14° per tick
	speed := 25 + rng.Float64()*55       // km/h

	km := speed * interval.Hours()
	u.lat += km / 111.0 * math.Sin(u.dir)
	u.lng += km / (111.0 * math.Cos(u.lat*math.Pi/180)) * math.Cos(u.dir)

	if u.lat < minLat || u.lat > maxLat {
		u.dir = -u.dir
		u.lat = math.Max(minLat, math.Min(maxLat, u.lat))
	}
	if u.lng < minLng || u.lng > maxLng {
		u.dir = math.Pi - u.dir
		u.lng = math.Max(minLng, math.Min(maxLng, u.lng))
	}

	return speed
}

func listVehicles(client *http.Client, api, token string) ([]apiVehicle, error) {
	req, err := http.NewRequest(http.MethodGet, api+"/api/vehicles", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/vehicles: %s (%s)", resp.Status, bytes.TrimSpace(raw))
	}

	var parsed listResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode fleet: %w", err)
	}

	return parsed.Data.Vehicles, nil
}

func createVehicle(client *http.Client, api, code, name, token string) (apiVehicle, error) {
	var parsed struct {
		Data apiVehicle `json:"data"`
	}
	body := map[string]string{"code": code, "name": name, "kind": "truck"}
	if err := postJSON(client, api+"/api/vehicles", body, &parsed, token); err != nil {
		return apiVehicle{}, err
	}

	return parsed.Data, nil
}

// login exchanges credentials for a bearer token.
func login(client *http.Client, api, email, password string) (string, error) {
	var parsed struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	body := map[string]string{"email": email, "password": password}
	if err := postJSON(client, api+"/api/auth/login", body, &parsed, ""); err != nil {
		return "", err
	}
	if parsed.Data.Token == "" {
		return "", fmt.Errorf("login returned no token")
	}

	return parsed.Data.Token, nil
}

func postJSON(client *http.Client, url string, body any, out any, token string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{
			status: resp.StatusCode,
			err:    fmt.Errorf("POST %s: %s (%s)", url, resp.Status, bytes.TrimSpace(raw)),
		}
	}

	if out == nil {
		return nil
	}

	return json.Unmarshal(raw, out)
}
