package domain

// BDDistricts is the static, canonical list of Bangladesh's 64 districts and
// their sub-districts (upazilas/thanas), served via GET /bd-districts so the
// admin panel and the mobile app share one source instead of each
// hand-maintaining a duplicate copy.
//
// There is no database table backing this — districts/upazilas are a fixed
// administrative structure that essentially never changes, unlike
// University/Department. IDs are stable lowercase-hyphen slugs (district
// slug, or "<district-slug>-<upazila-slug>" for sub-districts) so they never
// collide across districts and never need renumbering.
//
// DATA ENTRY NOTE: this list was populated from general knowledge of
// Bangladesh's administrative divisions and should be cross-checked against
// an authoritative source (Bangladesh Bureau of Statistics geo-codes, or the
// national web portal's district/upazila directory) before this is treated
// as production-accurate — some upazila names/counts may need correction.

// BDSubDistrict is a single upazila (sub-district) within a District.
type BDSubDistrict struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// BDDistrict is one of Bangladesh's 64 districts, with its Division and
// the upazilas within it.
type BDDistrict struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Division     string          `json:"division"`
	SubDistricts []BDSubDistrict `json:"sub_districts"`
}

func district(id, name, division string, upazilas ...string) BDDistrict {
	subs := make([]BDSubDistrict, len(upazilas))
	for i, u := range upazilas {
		subs[i] = BDSubDistrict{ID: id + "-" + slugify(u), Name: u}
	}
	return BDDistrict{ID: id, Name: name, Division: division, SubDistricts: subs}
}

// slugify is a minimal, dependency-free ascii-lowercase-hyphen slugger —
// upazila names here are plain ASCII with spaces/apostrophes/periods only.
func slugify(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == ' ' || r == '-':
			out = append(out, '-')
		// drop apostrophes, periods, etc.
		}
	}
	return string(out)
}

var BDDistricts = []BDDistrict{
	// --- Dhaka Division (13) ---
	district("dhaka", "Dhaka", "Dhaka",
		"Dhamrai", "Dohar", "Keraniganj", "Nawabganj", "Savar",
		"Tejgaon", "Mirpur", "Mohammadpur", "Gulshan", "Sabujbagh", "Demra"),
	district("faridpur", "Faridpur", "Dhaka",
		"Alfadanga", "Bhanga", "Boalmari", "Charbhadrasan", "Faridpur Sadar", "Madhukhali", "Nagarkanda", "Sadarpur", "Saltha"),
	district("gazipur", "Gazipur", "Dhaka",
		"Gazipur Sadar", "Kaliakair", "Kaliganj", "Kapasia", "Sreepur"),
	district("gopalganj", "Gopalganj", "Dhaka",
		"Gopalganj Sadar", "Kashiani", "Kotalipara", "Muksudpur", "Tungipara"),
	district("kishoreganj", "Kishoreganj", "Dhaka",
		"Austagram", "Bajitpur", "Bhairab", "Hossainpur", "Itna", "Karimganj", "Katiadi", "Kishoreganj Sadar",
		"Kuliarchar", "Mithamain", "Nikli", "Pakundia", "Tarail"),
	district("madaripur", "Madaripur", "Dhaka",
		"Kalkini", "Madaripur Sadar", "Rajoir", "Shibchar"),
	district("manikganj", "Manikganj", "Dhaka",
		"Daulatpur", "Ghior", "Harirampur", "Manikganj Sadar", "Saturia", "Shibalaya", "Singair"),
	district("munshiganj", "Munshiganj", "Dhaka",
		"Gazaria", "Lohajang", "Munshiganj Sadar", "Sirajdikhan", "Sreenagar", "Tongibari"),
	district("narayanganj", "Narayanganj", "Dhaka",
		"Araihazar", "Bandar", "Narayanganj Sadar", "Rupganj", "Sonargaon"),
	district("narsingdi", "Narsingdi", "Dhaka",
		"Belabo", "Monohardi", "Narsingdi Sadar", "Palash", "Raipura", "Shibpur"),
	district("rajbari", "Rajbari", "Dhaka",
		"Baliakandi", "Goalandaghat", "Pangsha", "Rajbari Sadar", "Kalukhali"),
	district("shariatpur", "Shariatpur", "Dhaka",
		"Bhedarganj", "Damudya", "Gosairhat", "Naria", "Shariatpur Sadar", "Zajira"),
	district("tangail", "Tangail", "Dhaka",
		"Basail", "Bhuapur", "Delduar", "Dhanbari", "Ghatail", "Gopalpur", "Kalihati", "Madhupur",
		"Mirzapur", "Nagarpur", "Sakhipur", "Tangail Sadar"),

	// --- Chattogram Division (11) ---
	district("bandarban", "Bandarban", "Chattogram",
		"Alikadam", "Bandarban Sadar", "Lama", "Naikhongchhari", "Rowangchhari", "Ruma", "Thanchi"),
	district("brahmanbaria", "Brahmanbaria", "Chattogram",
		"Akhaura", "Ashuganj", "Bancharampur", "Bijoynagar", "Brahmanbaria Sadar", "Kasba", "Nabinagar", "Nasirnagar", "Sarail"),
	district("chandpur", "Chandpur", "Chattogram",
		"Chandpur Sadar", "Faridganj", "Haimchar", "Haziganj", "Kachua", "Matlab Dakshin", "Matlab Uttar", "Shahrasti"),
	district("chattogram", "Chattogram", "Chattogram",
		"Anwara", "Banshkhali", "Boalkhali", "Chandanaish", "Fatikchhari", "Hathazari", "Lohagara", "Mirsharai",
		"Patiya", "Rangunia", "Raozan", "Sandwip", "Satkania", "Sitakunda"),
	district("cumilla", "Cumilla", "Chattogram",
		"Barura", "Brahmanpara", "Burichang", "Chandina", "Chauddagram", "Daudkandi", "Debidwar", "Homna",
		"Laksam", "Meghna", "Monohorgonj", "Muradnagar", "Nangalkot", "Cumilla Sadar", "Cumilla Sadar Dakshin", "Titas"),
	district("coxsbazar", "Cox's Bazar", "Chattogram",
		"Chakaria", "Cox's Bazar Sadar", "Kutubdia", "Maheshkhali", "Pekua", "Ramu", "Teknaf", "Ukhia"),
	district("feni", "Feni", "Chattogram",
		"Chhagalnaiya", "Daganbhuiyan", "Feni Sadar", "Fulgazi", "Parshuram", "Sonagazi"),
	district("khagrachhari", "Khagrachhari", "Chattogram",
		"Dighinala", "Khagrachhari Sadar", "Lakshmichhari", "Mahalchhari", "Manikchhari", "Matiranga", "Panchhari", "Ramgarh"),
	district("lakshmipur", "Lakshmipur", "Chattogram",
		"Kamalnagar", "Lakshmipur Sadar", "Raipur", "Ramganj", "Ramgati"),
	district("noakhali", "Noakhali", "Chattogram",
		"Begumganj", "Chatkhil", "Companiganj", "Hatiya", "Kabirhat", "Noakhali Sadar", "Senbagh", "Sonaimuri", "Subarnachar"),
	district("rangamati", "Rangamati", "Chattogram",
		"Baghaichhari", "Barkal", "Belaichhari", "Juraichhari", "Kaptai", "Kawkhali", "Langadu", "Naniarchar", "Rajasthali", "Rangamati Sadar"),

	// --- Rajshahi Division (8) ---
	district("bogura", "Bogura", "Rajshahi",
		"Adamdighi", "Bogura Sadar", "Dhunat", "Dhupchanchia", "Gabtali", "Kahaloo", "Nandigram",
		"Sariakandi", "Shajahanpur", "Sherpur", "Shibganj", "Sonatola"),
	district("joypurhat", "Joypurhat", "Rajshahi",
		"Akkelpur", "Joypurhat Sadar", "Kalai", "Khetlal", "Panchbibi"),
	district("naogaon", "Naogaon", "Rajshahi",
		"Atrai", "Badalgachhi", "Dhamoirhat", "Manda", "Mahadebpur", "Naogaon Sadar", "Niamatpur",
		"Patnitala", "Porsha", "Raninagar", "Sapahar"),
	district("natore", "Natore", "Rajshahi",
		"Bagatipara", "Baraigram", "Gurudaspur", "Lalpur", "Natore Sadar", "Singra", "Naldanga"),
	district("chapainawabganj", "Chapainawabganj", "Rajshahi",
		"Bholahat", "Gomastapur", "Nachole", "Chapainawabganj Sadar", "Shibganj"),
	district("pabna", "Pabna", "Rajshahi",
		"Atgharia", "Bera", "Bhangura", "Chatmohar", "Faridpur", "Ishwardi", "Pabna Sadar", "Santhia", "Sujanagar"),
	district("rajshahi", "Rajshahi", "Rajshahi",
		"Bagha", "Bagmara", "Charghat", "Durgapur", "Godagari", "Mohanpur", "Paba", "Puthia", "Tanore"),
	district("sirajganj", "Sirajganj", "Rajshahi",
		"Belkuchi", "Chauhali", "Kamarkhanda", "Kazipur", "Raiganj", "Shahjadpur", "Sirajganj Sadar", "Tarash", "Ullapara"),

	// --- Khulna Division (10) ---
	district("bagerhat", "Bagerhat", "Khulna",
		"Bagerhat Sadar", "Chitalmari", "Fakirhat", "Kachua", "Mollahat", "Mongla", "Morrelganj", "Rampal", "Sarankhola"),
	district("chuadanga", "Chuadanga", "Khulna",
		"Alamdanga", "Chuadanga Sadar", "Damurhuda", "Jibannagar"),
	district("jashore", "Jashore", "Khulna",
		"Abhaynagar", "Bagherpara", "Chaugachha", "Jashore Sadar", "Jhikargachha", "Keshabpur", "Manirampur", "Sharsha"),
	district("jhenaidah", "Jhenaidah", "Khulna",
		"Harinakunda", "Jhenaidah Sadar", "Kaliganj", "Kotchandpur", "Maheshpur", "Shailkupa"),
	district("khulna", "Khulna", "Khulna",
		"Batiaghata", "Dacope", "Dumuria", "Dighalia", "Koyra", "Paikgachha", "Phultala", "Rupsa", "Terokhada"),
	district("kushtia", "Kushtia", "Khulna",
		"Bheramara", "Daulatpur", "Khoksa", "Kumarkhali", "Kushtia Sadar", "Mirpur"),
	district("magura", "Magura", "Khulna",
		"Magura Sadar", "Mohammadpur", "Shalikha", "Sreepur"),
	district("meherpur", "Meherpur", "Khulna",
		"Gangni", "Meherpur Sadar", "Mujibnagar"),
	district("narail", "Narail", "Khulna",
		"Kalia", "Lohagara", "Narail Sadar"),
	district("satkhira", "Satkhira", "Khulna",
		"Assasuni", "Debhata", "Kalaroa", "Kaliganj", "Satkhira Sadar", "Shyamnagar", "Tala"),

	// --- Barisal Division (6) ---
	district("barguna", "Barguna", "Barisal",
		"Amtali", "Bamna", "Barguna Sadar", "Betagi", "Patharghata", "Taltali"),
	district("barisal", "Barisal", "Barisal",
		"Agailjhara", "Babuganj", "Bakerganj", "Banaripara", "Barisal Sadar", "Gaurnadi", "Hizla", "Mehendiganj", "Muladi", "Wazirpur"),
	district("bhola", "Bhola", "Barisal",
		"Bhola Sadar", "Burhanuddin", "Char Fasson", "Daulatkhan", "Lalmohan", "Manpura", "Tazumuddin"),
	district("jhalokati", "Jhalokati", "Barisal",
		"Jhalokati Sadar", "Kathalia", "Nalchity", "Rajapur"),
	district("patuakhali", "Patuakhali", "Barisal",
		"Bauphal", "Dashmina", "Dumki", "Galachipa", "Kalapara", "Mirzaganj", "Patuakhali Sadar", "Rangabali"),
	district("pirojpur", "Pirojpur", "Barisal",
		"Bhandaria", "Kawkhali", "Mathbaria", "Nazirpur", "Nesarabad (Swarupkati)", "Pirojpur Sadar", "Zianagar"),

	// --- Sylhet Division (4) ---
	district("habiganj", "Habiganj", "Sylhet",
		"Ajmiriganj", "Bahubal", "Baniachong", "Chunarughat", "Habiganj Sadar", "Lakhai", "Madhabpur", "Nabiganj", "Shayestaganj"),
	district("moulvibazar", "Moulvibazar", "Sylhet",
		"Barlekha", "Juri", "Kamalganj", "Kulaura", "Moulvibazar Sadar", "Rajnagar", "Sreemangal"),
	district("sunamganj", "Sunamganj", "Sylhet",
		"Bishwamvarpur", "Chhatak", "Derai", "Dharampasha", "Dowarabazar", "Jagannathpur", "Jamalganj",
		"Sulla", "Sunamganj Sadar", "Shantiganj", "Tahirpur"),
	district("sylhet", "Sylhet", "Sylhet",
		"Balaganj", "Beanibazar", "Bishwanath", "Companiganj", "Fenchuganj", "Golapganj", "Gowainghat",
		"Jaintiapur", "Kanaighat", "Osmani Nagar", "Sylhet Sadar", "Zakiganj"),

	// --- Rangpur Division (8) ---
	district("dinajpur", "Dinajpur", "Rangpur",
		"Birampur", "Birganj", "Biral", "Bochaganj", "Chirirbandar", "Dinajpur Sadar", "Fulbari",
		"Ghoraghat", "Hakimpur", "Kaharole", "Khansama", "Nawabganj", "Parbatipur"),
	district("gaibandha", "Gaibandha", "Rangpur",
		"Fulchhari", "Gaibandha Sadar", "Gobindaganj", "Palashbari", "Sadullapur", "Saghata", "Sundarganj"),
	district("kurigram", "Kurigram", "Rangpur",
		"Bhurungamari", "Char Rajibpur", "Chilmari", "Kurigram Sadar", "Nageshwari", "Phulbari", "Rajarhat", "Raomari", "Ulipur"),
	district("lalmonirhat", "Lalmonirhat", "Rangpur",
		"Aditmari", "Hatibandha", "Kaliganj", "Lalmonirhat Sadar", "Patgram"),
	district("nilphamari", "Nilphamari", "Rangpur",
		"Dimla", "Domar", "Jaldhaka", "Kishoreganj", "Nilphamari Sadar", "Saidpur"),
	district("panchagarh", "Panchagarh", "Rangpur",
		"Atwari", "Boda", "Debiganj", "Panchagarh Sadar", "Tetulia"),
	district("rangpur", "Rangpur", "Rangpur",
		"Badarganj", "Gangachara", "Kaunia", "Mithapukur", "Pirgachha", "Pirganj", "Rangpur Sadar", "Taraganj"),
	district("thakurgaon", "Thakurgaon", "Rangpur",
		"Baliadangi", "Haripur", "Pirganj", "Ranisankail", "Thakurgaon Sadar"),

	// --- Mymensingh Division (4) ---
	district("jamalpur", "Jamalpur", "Mymensingh",
		"Bakshiganj", "Dewanganj", "Islampur", "Jamalpur Sadar", "Madarganj", "Melandaha", "Sarishabari"),
	district("mymensingh", "Mymensingh", "Mymensingh",
		"Bhaluka", "Dhobaura", "Fulbaria", "Gaffargaon", "Gauripur", "Haluaghat", "Ishwarganj",
		"Mymensingh Sadar", "Muktagachha", "Nandail", "Phulpur", "Trishal", "Tara khanda"),
	district("netrokona", "Netrokona", "Mymensingh",
		"Atpara", "Barhatta", "Durgapur", "Kalmakanda", "Kendua", "Khaliajuri", "Madan",
		"Mohanganj", "Netrokona Sadar", "Purbadhala"),
	district("sherpur", "Sherpur", "Mymensingh",
		"Jhenaigati", "Nakla", "Nalitabari", "Sherpur Sadar", "Sreebardi"),
}
