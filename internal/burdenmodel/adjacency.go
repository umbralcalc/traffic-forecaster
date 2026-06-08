package burdenmodel

// boroughAdjacency is a nearest-neighbour graph over the London boroughs,
// derived from borough centroids (mean works_location_coordinates, BNG) via
// symmetric K=4 nearest neighbours. Generated once from a Street Manager
// extract; committed as small derived reference data.
var boroughAdjacency = map[string][]string{
	"Barking & Dagenham":   {"Bexley", "Greenwich", "Havering", "Newham", "Redbridge"},
	"Barnet":               {"Brent", "Camden", "Enfield", "Haringey", "Harrow"},
	"Bexley":               {"Barking & Dagenham", "Bromley", "Greenwich", "Havering", "Newham"},
	"Brent":                {"Barnet", "Ealing", "Harrow", "Hillingdon", "Kensington & Chelsea"},
	"Bromley":              {"Bexley", "Croydon", "Greenwich", "Lewisham", "Southwark"},
	"Camden":               {"Barnet", "City of London", "Hammersmith & Fulham", "Haringey", "Islington", "Kensington & Chelsea", "Westminster"},
	"City of London":       {"Camden", "Hackney", "Islington", "Southwark", "Tower Hamlets", "Westminster"},
	"Croydon":              {"Bromley", "Lambeth", "Merton", "Sutton"},
	"Ealing":               {"Brent", "Harrow", "Hillingdon", "Hounslow", "Richmond upon Thames"},
	"Enfield":              {"Barnet", "Hackney", "Haringey", "Waltham Forest"},
	"Greenwich":            {"Barking & Dagenham", "Bexley", "Bromley", "Lewisham", "Newham", "Tower Hamlets"},
	"Hackney":              {"City of London", "Enfield", "Haringey", "Islington", "Tower Hamlets", "Waltham Forest"},
	"Hammersmith & Fulham": {"Camden", "Kensington & Chelsea", "Richmond upon Thames", "Wandsworth", "Westminster"},
	"Haringey":             {"Barnet", "Camden", "Enfield", "Hackney", "Islington", "Waltham Forest"},
	"Harrow":               {"Barnet", "Brent", "Ealing", "Hillingdon"},
	"Havering":             {"Barking & Dagenham", "Bexley", "Newham", "Redbridge"},
	"Hillingdon":           {"Brent", "Ealing", "Harrow", "Hounslow"},
	"Hounslow":             {"Ealing", "Hillingdon", "Kingston upon Thames", "Richmond upon Thames"},
	"Islington":            {"Camden", "City of London", "Hackney", "Haringey", "Westminster"},
	"Kensington & Chelsea": {"Brent", "Camden", "Hammersmith & Fulham", "Wandsworth", "Westminster"},
	"Kingston upon Thames": {"Hounslow", "Merton", "Richmond upon Thames", "Sutton", "Wandsworth"},
	"Lambeth":              {"Croydon", "Lewisham", "Merton", "Southwark", "Wandsworth"},
	"Lewisham":             {"Bromley", "Greenwich", "Lambeth", "Southwark"},
	"Merton":               {"Croydon", "Kingston upon Thames", "Lambeth", "Sutton", "Wandsworth"},
	"Newham":               {"Barking & Dagenham", "Bexley", "Greenwich", "Havering", "Redbridge", "Tower Hamlets", "Waltham Forest"},
	"Redbridge":            {"Barking & Dagenham", "Havering", "Newham", "Waltham Forest"},
	"Richmond upon Thames": {"Ealing", "Hammersmith & Fulham", "Hounslow", "Kingston upon Thames"},
	"Southwark":            {"Bromley", "City of London", "Lambeth", "Lewisham", "Tower Hamlets"},
	"Sutton":               {"Croydon", "Kingston upon Thames", "Merton", "Wandsworth"},
	"Tower Hamlets":        {"City of London", "Greenwich", "Hackney", "Newham", "Southwark"},
	"Waltham Forest":       {"Enfield", "Hackney", "Haringey", "Newham", "Redbridge"},
	"Wandsworth":           {"Hammersmith & Fulham", "Kensington & Chelsea", "Kingston upon Thames", "Lambeth", "Merton", "Sutton"},
	"Westminster":          {"Camden", "City of London", "Hammersmith & Fulham", "Islington", "Kensington & Chelsea"},
}

// Neighbours returns the adjacent boroughs of b (empty if none/unknown).
func Neighbours(b string) []string { return boroughAdjacency[b] }
