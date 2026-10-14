package main

import "fmt"

var firstNames = []string{
	"Olivia", "Liam", "Emma", "Noah", "Ava", "Oliver", "Sophia", "Elijah",
	"Isabella", "James", "Mia", "William", "Amelia", "Benjamin", "Harper", "Lucas",
	"Evelyn", "Henry", "Abigail", "Theodore", "Emily", "Jack", "Ella", "Levi",
	"Elizabeth", "Alexander", "Camila", "Jackson", "Luna", "Mateo", "Sofia", "Daniel",
	"Avery", "Michael", "Mila", "Mason", "Aria", "Sebastian", "Scarlett", "Ethan",
	"Penelope", "Logan", "Layla", "Owen", "Chloe", "Samuel", "Victoria", "Jacob",
	"Madison", "Asher", "Eleanor", "Aiden", "Grace", "John", "Nora", "Joseph",
	"Riley", "Wyatt", "Zoey", "David", "Hannah", "Leo", "Hazel", "Luke",
	"Priya", "Wei", "Aisha", "Diego", "Yuki", "Kofi", "Ingrid", "Rafael",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis",
	"Rodriguez", "Martinez", "Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas",
	"Taylor", "Moore", "Jackson", "Martin", "Lee", "Perez", "Thompson", "White",
	"Harris", "Sanchez", "Clark", "Ramirez", "Lewis", "Robinson", "Walker", "Young",
	"Allen", "King", "Wright", "Scott", "Torres", "Nguyen", "Hill", "Flores",
	"Green", "Adams", "Nelson", "Baker", "Hall", "Rivera", "Campbell", "Mitchell",
	"Carter", "Roberts", "Patel", "Kim", "Chen", "Okafor", "Larsen", "Novak",
	"Schmidt", "Rossi", "Silva", "Tanaka", "Murphy", "Cohen", "Reyes", "Park",
}

// usCity is a real city / state / ZIP combination, so Shopify's address
// validation accepts generated addresses.
type usCity struct {
	City, Province, Zip string
}

var usCities = []usCity{
	{"Chicago", "IL", "60614"},
	{"Austin", "TX", "78701"},
	{"Seattle", "WA", "98101"},
	{"Denver", "CO", "80202"},
	{"Portland", "OR", "97205"},
	{"Boston", "MA", "02108"},
	{"New York", "NY", "10001"},
	{"Brooklyn", "NY", "11201"},
	{"San Francisco", "CA", "94103"},
	{"Los Angeles", "CA", "90012"},
	{"San Diego", "CA", "92101"},
	{"Miami", "FL", "33130"},
	{"Orlando", "FL", "32801"},
	{"Atlanta", "GA", "30303"},
	{"Nashville", "TN", "37203"},
	{"Minneapolis", "MN", "55401"},
	{"Phoenix", "AZ", "85004"},
	{"Philadelphia", "PA", "19103"},
	{"Pittsburgh", "PA", "15222"},
	{"Columbus", "OH", "43215"},
	{"Madison", "WI", "53703"},
	{"Salt Lake City", "UT", "84111"},
	{"Raleigh", "NC", "27601"},
	{"Kansas City", "MO", "64105"},
	{"New Orleans", "LA", "70112"},
	{"Ann Arbor", "MI", "48104"},
	{"Burlington", "VT", "05401"},
	{"Boise", "ID", "83702"},
	{"Richmond", "VA", "23219"},
	{"Omaha", "NE", "68102"},
}

var streetNames = []string{
	"Maple Ave", "Oak St", "Pine St", "Cedar Ln", "Elm St", "Washington Ave",
	"Lake St", "Hill Rd", "Park Ave", "Main St", "Jefferson St", "Sunset Blvd",
	"River Rd", "Highland Ave", "Chestnut St", "Walnut St", "Spring St", "Franklin St",
	"Lincoln Ave", "Willow Way", "Madison Ave", "Ridge Rd", "Harbor Dr", "Mill St",
}

type catalogProduct struct {
	Title    string
	Variants []string
	Cents    int64
}

// syntheticProducts back the built-in 40-variant catalog that plan uses when
// no store is given.
var syntheticProducts = []catalogProduct{
	{"Everyday Tee", []string{"S", "M", "L", "XL"}, 2800},
	{"Heavyweight Hoodie", []string{"S", "M", "L", "XL"}, 7200},
	{"Merino Crew Socks", []string{"Charcoal", "Oat", "Navy"}, 1800},
	{"Canvas Tote", []string{"Natural", "Black"}, 2400},
	{"Ceramic Pour-Over", []string{"White", "Speckled"}, 3800},
	{"Single-Origin Coffee 12oz", []string{"Whole Bean", "Ground"}, 1900},
	{"Enamel Camp Mug", []string{"Cream", "Navy"}, 1600},
	{"Linen Throw Pillow", []string{"Sand", "Sage", "Rust"}, 4500},
	{"Wool Beanie", []string{"Black", "Heather Grey"}, 3200},
	{"Leather Card Wallet", []string{"Tan", "Black"}, 5400},
	{"Insulated Bottle 24oz", []string{"Forest", "Slate", "Coral"}, 3400},
	{"Gift Card", []string{"$25", "$50", "$100"}, 2500},
	{"Field Notebook 3-Pack", []string{"Default Title"}, 1400},
	{"Soy Candle", []string{"Cedar", "Fig", "Sea Salt"}, 2900},
	{"Waxed Canvas Backpack", []string{"Default Title"}, 14800},
	{"Bamboo Cutting Board", []string{"Default Title"}, 4200},
	{"Trail Running Cap", []string{"Black", "Olive"}, 3000},
}

// SyntheticCatalog returns the built-in 40-variant catalog.
func SyntheticCatalog() []Variant {
	var out []Variant
	id := 44000000001
	for _, p := range syntheticProducts {
		for j, v := range p.Variants {
			price := p.Cents
			if p.Title == "Gift Card" {
				price = []int64{2500, 5000, 10000}[j]
			} else if v == "XL" {
				price += 200
			}
			out = append(out, Variant{
				ID:         fmt.Sprintf("gid://shopify/ProductVariant/%d", id),
				Title:      displayTitle(p.Title, v),
				PriceCents: price,
			})
			id++
		}
	}
	return out
}
