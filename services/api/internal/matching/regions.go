package matching

import "slices"

// Region membership for remote-scope regions produced by discovery
// (sources.regionNames). "maybe" members are countries that postings
// inconsistently include (e.g. Mexico in "North America"); for those the
// check is UNKNOWN rather than PASS or FAIL.
var (
	europe = []string{"AL", "AD", "AT", "BA", "BE", "BG", "BY", "CH", "CY", "CZ", "DE", "DK", "EE", "ES", "FI", "FR",
		"GB", "GR", "HR", "HU", "IE", "IS", "IT", "LI", "LT", "LU", "LV", "MC", "MD", "ME", "MK", "MT", "NL", "NO", "PL",
		"PT", "RO", "RS", "SE", "SI", "SK", "SM", "UA", "XK"}
	middleEast = []string{"AE", "BH", "IL", "IQ", "JO", "KW", "LB", "OM", "PS", "QA", "SA", "YE"}
	africa     = []string{"AO", "BF", "BJ", "BW", "CD", "CI", "CM", "DZ", "EG", "ET", "GH", "KE", "MA", "MG", "MU", "MW",
		"MZ", "NA", "NG", "RW", "SN", "SD", "TN", "TZ", "UG", "ZA", "ZM", "ZW"}
	latam = []string{"AR", "BO", "BR", "CL", "CO", "CR", "CU", "DO", "EC", "GT", "HN", "JM", "MX", "NI", "PA", "PE",
		"PR", "PY", "SV", "TT", "UY", "VE"}
	asia = []string{"BD", "CN", "HK", "ID", "IN", "JP", "KH", "KR", "LK", "MM", "MN", "MY", "NP", "PH", "PK", "SG",
		"TH", "TW", "VN"}
	apac = append(slices.Clone(asia), "AU", "NZ")

	regionMembers = map[string][]string{
		"Europe":        europe,
		"EMEA":          concat(europe, middleEast, africa, []string{"TR"}),
		"Middle East":   middleEast,
		"Africa":        africa,
		"North America": {"US", "CA"},
		"Latin America": latam,
		"Americas":      concat([]string{"US", "CA"}, latam),
		"Asia":          asia,
		"APAC":          apac,
	}
	regionMaybe = map[string][]string{
		"Europe":        {"TR", "RU", "GE", "AM", "AZ"},
		"North America": {"MX"},
		"Middle East":   {"EG", "TR", "IR"},
		"Asia":          {"TR", "AE", "SA", "IL", "QA"},
		"APAC":          {"TR"},
	}
)

func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// regionStatus reports whether any of countries lies in region:
// PASS (definitely), FAIL (definitely not) or UNKNOWN (ambiguous or unknown region).
func regionStatus(region string, countries []string) string {
	members, ok := regionMembers[region]
	if !ok {
		return CheckUnknown
	}
	maybe := false
	for _, c := range countries {
		if slices.Contains(members, c) {
			return CheckPass
		}
		if slices.Contains(regionMaybe[region], c) {
			maybe = true
		}
	}
	if maybe {
		return CheckUnknown
	}
	return CheckFail
}
