package conpty

type oscTitleFilter struct {
	prefix        []byte
	inTitle       bool
	inCommand     bool
	commandValue  int
	utf8Remaining int
}

func (f *oscTitleFilter) filter(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if f.utf8Remaining > 0 && b >= 0x80 && b <= 0xbf {
			f.utf8Remaining--
			if !f.inTitle {
				out = append(out, b)
			}
			continue
		}
		f.utf8Remaining = 0
		switch {
		case b >= 0xc2 && b <= 0xdf:
			f.utf8Remaining = 1
		case b >= 0xe0 && b <= 0xef:
			f.utf8Remaining = 2
		case b >= 0xf0 && b <= 0xf4:
			f.utf8Remaining = 3
		}
		if f.inTitle {
			switch b {
			case '\a', 0x18, 0x1a, 0x9c:
				f.inTitle = false
				out = append(out, b)
			case 0x1b:
				f.inTitle = false
				f.prefix = append(f.prefix, b)
			}
			continue
		}
		if f.inCommand {
			switch {
			case b >= '0' && b <= '9':
				out = append(out, b)
				f.commandValue = min(3, f.commandValue*10+int(b-'0'))
				continue
			case b == ';':
				out = append(out, b)
				f.inCommand = false
				f.inTitle = f.commandValue <= 2
				continue
			default:
				f.inCommand = false
			}
		}

		oscLen := 2
		if len(f.prefix) > 0 && f.prefix[0] == 0x9d {
			oscLen = 1
		}
		switch {
		case len(f.prefix) == 1 && f.prefix[0] == 0x1b && b == ']':
			f.prefix = append(f.prefix, b)
			continue
		case len(f.prefix) == oscLen && b >= '0' && b <= '9':
			out = append(out, f.prefix...)
			out = append(out, b)
			f.prefix = f.prefix[:0]
			f.inCommand = true
			f.commandValue = int(b - '0')
			continue
		}
		out = append(out, f.prefix...)
		f.prefix = f.prefix[:0]
		if b == 0x1b || b == 0x9d {
			f.prefix = append(f.prefix, b)
		} else {
			out = append(out, b)
		}
	}
	return out
}
