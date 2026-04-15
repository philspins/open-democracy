package store

type BillRow struct {
	ID               string
	Parliament       int
	Session          int
	Number           string
	Title            string
	ShortTitle       string
	BillType         string
	Chamber          string
	SponsorID        string
	SponsorName      string
	CurrentStage     string
	CurrentStatus    string
	Category         string
	SummaryAI        string
	SummaryLoP       string
	FullTextURL      string
	LegisInfoURL     string
	IntroducedDate   string
	LastActivityDate string
}

type DivisionRow struct {
	ID          string
	Parliament  int
	Session     int
	Number      int
	Date        string
	BillID      string
	BillNumber  string
	Description string
	Yeas        int
	Nays        int
	Paired      int
	Result      string
	Chamber     string
	SittingURL  string
}

type MemberRow struct {
	ID              string
	Name            string
	Party           string
	Riding          string
	Province        string
	Role            string
	PhotoURL        string
	Email           string
	Website         string
	Chamber         string
	Active          bool
	GovernmentLevel string // "federal" | "provincial"
}

type VoteRow struct {
	DivisionID     string
	Date           string
	BillID         string
	BillNumber     string
	Description    string
	Vote           string
	Result         string
	VotedWithParty bool
	PartyMajority  string
}

type MemberStats struct {
	TotalVotes   int
	PartyLinePct int
	RebelPct     int
	MissedPct    int
}

type ParliamentStatus struct {
	Status     string // "in_session" | "on_break"
	Label      string
	Detail     string
	Parliament int
	Session    int
}

type BillFilter struct {
	Search   string
	Stage    string
	Category string
	Chamber  string
	Page     int
	PerPage  int
}

type BillStageRow struct {
	Stage   string
	Chamber string
	Date    string
	Notes   string
}

type UserRow struct {
	ID                 string
	Email              string
	EmailVerified      bool
	Address            string
	FederalRidingID    string
	ProvincialRidingID string
	CreatedAt          string
	EmailDigest        string
}

type BillReactionCounts struct {
	BillID         string
	SupportCount   int
	OpposeCount    int
	NeutralCount   int
	TotalReactions int
	RefreshedAt    string
}
