package constants

type RiskBand string

const (
	RiskInformational RiskBand = "informational"
	RiskCaution       RiskBand = "caution"
	RiskElevated      RiskBand = "elevated"
	RiskInvalid       RiskBand = "invalid"
)

func ValidRiskBand(band RiskBand) bool {
	switch band {
	case RiskInformational, RiskCaution, RiskElevated, RiskInvalid:
		return true
	default:
		return false
	}
}

// ConfirmationRequired reports whether a supervisor must explicitly confirm a
// snapshot risk flag of this band before the assessment can be approved.
func ConfirmationRequired(band RiskBand) bool {
	switch band {
	case RiskCaution, RiskElevated, RiskInvalid:
		return true
	default:
		return false
	}
}
