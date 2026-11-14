package apptest

import (
	"net/http"
	"testing"
)

func TestProfileCRUDAndProvenance(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("profile@example.com")

	r := c.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{
		"employer": "Acme", "title": "Backend Engineer", "start_date": "2021-03-01", "is_current": true,
		"achievements": []string{"Built the billing service"},
	})
	if r.Code != http.StatusCreated {
		t.Fatalf("create experience: %d %s", r.Code, r.Body)
	}
	var exp struct {
		ID           string `json:"id"`
		Source       string `json:"source"`
		Verification string `json:"verification"`
	}
	r.JSON(t, &exp)
	if exp.Source != "USER_ENTERED" || exp.Verification != "VERIFIED" {
		t.Fatalf("user-entered facts must be verified: %+v", exp)
	}

	r = c.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{"employer": "", "title": "x", "start_date": "2022-01-01", "end_date": "2021-01-01"})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid experience accepted: %d", r.Code)
	}

	for _, name := range []string{"Go", "PostgreSQL", "go "} {
		r = c.Do(http.MethodPost, "/api/v1/profiles/me/skills", map[string]any{"name": name})
		if name == "go " {
			if r.Code != http.StatusConflict {
				t.Fatalf("duplicate skill (normalised) accepted: %d", r.Code)
			}
			continue
		}
		if r.Code != http.StatusCreated {
			t.Fatalf("skill %s: %d %s", name, r.Code, r.Body)
		}
	}

	r = c.Do(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{
		"headline": "Backend engineer", "location_country": "ke", "links": []map[string]string{{"label": "GitHub", "url": "javascript:alert(1)"}},
	})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("javascript: URL accepted: %d %s", r.Code, r.Body)
	}
	r = c.Do(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{
		"headline": "Backend engineer", "location_country": "ke", "links": []map[string]string{{"label": "GitHub", "url": "https://github.com/x"}},
	})
	if r.Code != http.StatusOK {
		t.Fatalf("basics: %d %s", r.Code, r.Body)
	}

	r = c.Do(http.MethodPut, "/api/v1/profiles/me/preferences", map[string]any{
		"target_roles": []string{"Backend Engineer"}, "remote_mode": "REMOTE_ONLY", "work_eligible_countries": []string{"ke"},
		"min_salary": 4000, "salary_currency": "usd", "salary_period": "MONTH",
	})
	if r.Code != http.StatusOK {
		t.Fatalf("preferences: %d %s", r.Code, r.Body)
	}
	r = c.Do(http.MethodPut, "/api/v1/profiles/me/preferences", map[string]any{"remote_mode": "REMOTE_ONLY", "min_salary": 4000})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("partial salary accepted: %d", r.Code)
	}

	var p struct {
		Basics struct {
			LocationCountry string `json:"location_country"`
		} `json:"basics"`
		Preferences struct {
			WorkEligibleCountries []string `json:"work_eligible_countries"`
			SalaryCurrency        string   `json:"salary_currency"`
		} `json:"preferences"`
		Completeness struct {
			Score   int                    `json:"score"`
			Missing []struct{ Key string } `json:"missing"`
		} `json:"completeness"`
	}
	c.Do(http.MethodGet, "/api/v1/profiles/me", nil).JSON(t, &p)
	if p.Basics.LocationCountry != "KE" || p.Preferences.WorkEligibleCountries[0] != "KE" || p.Preferences.SalaryCurrency != "USD" {
		t.Fatalf("normalisation failed: %+v", p)
	}
	// headline 10 + country 10 + experience 20 + target roles 10 + eligibility 15 + salary 5 + verification 10 = 80
	if p.Completeness.Score != 80 {
		t.Fatalf("completeness score = %d, missing=%+v", p.Completeness.Score, p.Completeness.Missing)
	}

	if r := c.Do(http.MethodDelete, "/api/v1/profiles/me/experiences/"+exp.ID, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/profiles/me/unknown-kind/"+exp.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("unknown kind: %d", r.Code)
	}
}

func TestProfileIsolationBetweenUsers(t *testing.T) {
	app := New(t, Options{})
	alice := app.Client(t)
	alice.Register("alice@example.com")
	bob := app.Client(t)
	bob.Register("bob@example.com")

	var exp struct{ ID string }
	alice.Do(http.MethodPost, "/api/v1/profiles/me/experiences", map[string]any{"employer": "Secret Corp", "title": "Engineer"}).JSON(t, &exp)

	// Bob cannot read, edit, verify or delete Alice's fact; all look like 404.
	if r := bob.Do(http.MethodPut, "/api/v1/profiles/me/experiences/"+exp.ID, map[string]any{"employer": "Hacked", "title": "x"}); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user update: %d", r.Code)
	}
	if r := bob.Do(http.MethodPut, "/api/v1/profiles/me/experiences/"+exp.ID+"/verification", map[string]string{"verification": "REJECTED"}); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user verify: %d", r.Code)
	}
	if r := bob.Do(http.MethodDelete, "/api/v1/profiles/me/experiences/"+exp.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete: %d", r.Code)
	}
	var bp struct{ Experiences []any }
	bob.Do(http.MethodGet, "/api/v1/profiles/me", nil).JSON(t, &bp)
	if len(bp.Experiences) != 0 {
		t.Fatal("bob sees alice's experience")
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM work_experiences WHERE employer = 'Secret Corp'`); n != 1 {
		t.Fatal("alice's record was modified")
	}
}
