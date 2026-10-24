package authz

func EffectiveLevel(levels ...AccessLevel) AccessLevel {
	eff := LevelNone
	for _, l := range levels {
		if !l.ValidGrant() {

			continue
		}
		if l > eff {
			eff = l
		}
	}
	return eff
}
