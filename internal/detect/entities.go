package detect

// Canonical entity types. The token format embeds the entity type, so a stable
// vocabulary keeps token labels identical regardless of which backend produced
// a finding. These names follow Presidio's convention (its backend is the
// identity mapping); the regex and DLP backends translate into these names.
const (
	EntityPerson       = "PERSON"
	EntityEmail        = "EMAIL_ADDRESS"
	EntityPhone        = "PHONE_NUMBER"
	EntityUSSSN        = "US_SSN"
	EntityCreditCard   = "CREDIT_CARD"
	EntityIPAddress    = "IP_ADDRESS"
	EntityLocation     = "LOCATION"
	EntityURL          = "URL"
	EntityDateTime     = "DATE_TIME"
	EntityIBAN         = "IBAN_CODE"
	EntityCryptoWallet = "CRYPTO"
)

// allowSet turns an entity allowlist into a lookup set. A nil/empty allowlist
// yields a nil set, which allowed treats as "allow everything".
func allowSet(entities []string) map[string]struct{} {
	if len(entities) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(entities))
	for _, e := range entities {
		set[e] = struct{}{}
	}
	return set
}

// allowed reports whether a canonical entity type passes the allowlist. A nil
// set means no restriction.
func allowed(set map[string]struct{}, entity string) bool {
	if set == nil {
		return true
	}
	_, ok := set[entity]
	return ok
}
