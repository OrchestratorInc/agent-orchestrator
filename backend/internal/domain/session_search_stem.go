package domain

// PorterStem returns the Porter stem of one lowercase English word: "failing",
// "failed" and "fails" all stem to "fail"; "signatures" to "signatur".
//
// This is M. F. Porter's algorithm ("An algorithm for suffix stripping",
// Program 14(3), 1980) transliterated from his reference C implementation,
// including that implementation's two documented departures from the paper
// (bli -> ble and logi -> log in step 2). It is verified against Porter's own
// 23,531-word test vocabulary (session_search_stem_test.go), which is the only
// meaningful definition of "a correct Porter stemmer".
//
// The index itself is not stemmed. FTS5's porter tokenizer stems prefix
// queries too, so a half-typed word in the palette ("authenti") would stop
// matching ("authentication" is indexed as "authent"). Instead the query side
// matches the stem as a prefix: "signatures" becomes "signatur"*, which FTS5
// scores as one term whose document frequency spans every form, the way a
// stemmed index would. See buildMatchExpressions.
//
// Words that are not all a-z (identifiers with digits, non-English text) are
// returned unchanged: the algorithm is defined for English letters only.
func PorterStem(word string) string {
	if len(word) <= 2 {
		return word
	}
	for index := 0; index < len(word); index++ {
		if word[index] < 'a' || word[index] > 'z' {
			return word
		}
	}
	s := porterStemmer{b: []byte(word), k: len(word) - 1}
	s.step1ab()
	if s.k > 0 {
		s.step1c()
		s.step2()
		s.step3()
		s.step4()
		s.step5()
	}
	return string(s.b[:s.k+1])
}

// porterStemmer holds the word being stemmed. b[0..k] is the current word and
// j is the boundary set by ends(), exactly as in the reference implementation.
type porterStemmer struct {
	b    []byte
	k, j int
}

// cons reports whether b[i] is a consonant. 'y' is a consonant at the start of
// a word or after a vowel.
func (s *porterStemmer) cons(i int) bool {
	switch s.b[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		return i == 0 || !s.cons(i-1)
	}
	return true
}

// m measures the number of consonant-vowel sequences in b[0..j]:
//
//	<c><v>       gives 0
//	<c>vc<v>     gives 1
//	<c>vcvc<v>   gives 2
func (s *porterStemmer) m() int {
	n, i := 0, 0
	for {
		if i > s.j {
			return n
		}
		if !s.cons(i) {
			break
		}
		i++
	}
	i++
	for {
		for {
			if i > s.j {
				return n
			}
			if s.cons(i) {
				break
			}
			i++
		}
		i++
		n++
		for {
			if i > s.j {
				return n
			}
			if !s.cons(i) {
				break
			}
			i++
		}
		i++
	}
}

// vowelInStem reports whether b[0..j] contains a vowel.
func (s *porterStemmer) vowelInStem() bool {
	for i := 0; i <= s.j; i++ {
		if !s.cons(i) {
			return true
		}
	}
	return false
}

// doubleC reports whether b[j-1..j] is a double consonant.
func (s *porterStemmer) doubleC(j int) bool {
	return j >= 1 && s.b[j] == s.b[j-1] && s.cons(j)
}

// cvc reports whether b[i-2..i] is consonant-vowel-consonant with the last not
// w, x or y; it restores an e in "hop(e)", not in "snow".
func (s *porterStemmer) cvc(i int) bool {
	if i < 2 || !s.cons(i) || s.cons(i-1) || !s.cons(i-2) {
		return false
	}
	switch s.b[i] {
	case 'w', 'x', 'y':
		return false
	}
	return true
}

// ends reports whether b[0..k] ends with suffix, setting j before it.
func (s *porterStemmer) ends(suffix string) bool {
	length := len(suffix)
	if length > s.k+1 {
		return false
	}
	if string(s.b[s.k-length+1:s.k+1]) != suffix {
		return false
	}
	s.j = s.k - length
	return true
}

// setTo replaces b[j+1..k] with replacement.
func (s *porterStemmer) setTo(replacement string) {
	s.b = append(s.b[:s.j+1], replacement...)
	s.k = s.j + len(replacement)
}

// r replaces the suffix when the stem before it has m() > 0.
func (s *porterStemmer) r(replacement string) {
	if s.m() > 0 {
		s.setTo(replacement)
	}
}

// step1ab removes plurals and -ed or -ing.
func (s *porterStemmer) step1ab() {
	if s.b[s.k] == 's' {
		switch {
		case s.ends("sses"):
			s.k -= 2
		case s.ends("ies"):
			s.setTo("i")
		case s.b[s.k-1] != 's':
			s.k--
		}
	}
	if s.ends("eed") {
		if s.m() > 0 {
			s.k--
		}
		return
	}
	if (s.ends("ed") || s.ends("ing")) && s.vowelInStem() {
		s.k = s.j
		switch {
		case s.ends("at"):
			s.setTo("ate")
		case s.ends("bl"):
			s.setTo("ble")
		case s.ends("iz"):
			s.setTo("ize")
		case s.doubleC(s.k):
			s.k--
			switch s.b[s.k] {
			case 'l', 's', 'z':
				s.k++
			}
		default:
			s.j = s.k
			if s.m() == 1 && s.cvc(s.k) {
				s.setTo("e")
			}
		}
	}
}

// step1c turns a terminal y into i when there is another vowel in the stem.
func (s *porterStemmer) step1c() {
	if s.ends("y") && s.vowelInStem() {
		s.b[s.k] = 'i'
	}
}

// step2 maps double suffixes to single ones: -ization -> -ize, and so on.
func (s *porterStemmer) step2() {
	if s.k < 1 {
		return
	}
	for _, rule := range porterStep2[s.b[s.k-1]] {
		if s.ends(rule[0]) {
			s.r(rule[1])
			return
		}
	}
}

var porterStep2 = map[byte][][2]string{
	'a': {{"ational", "ate"}, {"tional", "tion"}},
	'c': {{"enci", "ence"}, {"anci", "ance"}},
	'e': {{"izer", "ize"}},
	'l': {{"bli", "ble"}, {"alli", "al"}, {"entli", "ent"}, {"eli", "e"}, {"ousli", "ous"}},
	'o': {{"ization", "ize"}, {"ation", "ate"}, {"ator", "ate"}},
	's': {{"alism", "al"}, {"iveness", "ive"}, {"fulness", "ful"}, {"ousness", "ous"}},
	't': {{"aliti", "al"}, {"iviti", "ive"}, {"biliti", "ble"}},
	'g': {{"logi", "log"}},
}

// step3 handles -ic-, -full, -ness and similar.
func (s *porterStemmer) step3() {
	for _, rule := range porterStep3[s.b[s.k]] {
		if s.ends(rule[0]) {
			s.r(rule[1])
			return
		}
	}
}

var porterStep3 = map[byte][][2]string{
	'e': {{"icate", "ic"}, {"ative", ""}, {"alize", "al"}},
	'i': {{"iciti", "ic"}},
	'l': {{"ical", "ic"}, {"ful", ""}},
	's': {{"ness", ""}},
}

// step4 removes -ant, -ence and similar when m() > 1.
func (s *porterStemmer) step4() {
	if s.k < 1 {
		return
	}
	matched := false
	for _, suffix := range porterStep4[s.b[s.k-1]] {
		if !s.ends(suffix) {
			continue
		}
		if suffix == "ion" && (s.j < 0 || (s.b[s.j] != 's' && s.b[s.j] != 't')) {
			continue
		}
		matched = true
		break
	}
	if matched && s.m() > 1 {
		s.k = s.j
	}
}

var porterStep4 = map[byte][]string{
	'a': {"al"},
	'c': {"ance", "ence"},
	'e': {"er"},
	'i': {"ic"},
	'l': {"able", "ible"},
	'n': {"ant", "ement", "ment", "ent"},
	'o': {"ion", "ou"},
	's': {"ism"},
	't': {"ate", "iti"},
	'u': {"ous"},
	'v': {"ive"},
	'z': {"ize"},
}

// step5 removes a final -e when m() > 1, and -ll to -l when m() > 1.
func (s *porterStemmer) step5() {
	s.j = s.k
	if s.b[s.k] == 'e' {
		a := s.m()
		if a > 1 || (a == 1 && !s.cvc(s.k-1)) {
			s.k--
		}
	}
	if s.b[s.k] == 'l' && s.doubleC(s.k) && s.m() > 1 {
		s.k--
	}
}
