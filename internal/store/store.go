// Package store provides read-only query helpers for the Open Democracy web frontend.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store wraps a *sql.DB and exposes typed query methods.
type Store struct{ db *sql.DB }

// New returns a new Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// ── bill queries ──────────────────────────────────────────────────────────────

// ListBills returns a paginated list of bills matching the filter, plus total count.
func (s *Store) ListBills(f BillFilter) ([]BillRow, int, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if f.Search != "" {
		where = append(where, "(b.title LIKE ? OR b.number LIKE ? OR b.short_title LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
	}
	if f.Stage != "" {
		where = append(where, "b.current_stage = ?")
		args = append(args, f.Stage)
	}
	if f.Category != "" {
		where = append(where, "b.category = ?")
		args = append(args, f.Category)
	}
	if f.Chamber != "" {
		where = append(where, "b.chamber = ?")
		args = append(args, f.Chamber)
	}

	whereClause := strings.Join(where, " AND ")

	// Count
	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	err := s.db.QueryRow("SELECT COUNT(*) FROM bills b WHERE "+whereClause, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("ListBills count: %w", err)
	}

	if f.PerPage <= 0 {
		f.PerPage = 20
	}
	if f.Page < 1 {
		f.Page = 1
	}
	offset := (f.Page - 1) * f.PerPage

	query := `
		SELECT b.id, b.parliament, b.session, b.number, b.title,
		       COALESCE(b.short_title,''), COALESCE(b.bill_type,''), COALESCE(b.chamber,''),
		       COALESCE(b.sponsor_id,''), COALESCE(m.name,''),
		       COALESCE(b.current_stage,''), COALESCE(b.current_status,''),
		       COALESCE(b.category,''), COALESCE(b.summary_ai,''), COALESCE(b.summary_lop,''),
		       COALESCE(b.full_text_url,''), COALESCE(b.legisinfo_url,''),
		       COALESCE(b.introduced_date,''), COALESCE(b.last_activity_date,'')
		FROM bills b
		LEFT JOIN members m ON m.id = b.sponsor_id
		WHERE ` + whereClause + `
		ORDER BY b.last_activity_date DESC, b.id DESC
		LIMIT ? OFFSET ?`

	args = append(args, f.PerPage, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("ListBills query: %w", err)
	}
	defer rows.Close()

	var bills []BillRow
	for rows.Next() {
		var b BillRow
		if err := rows.Scan(
			&b.ID, &b.Parliament, &b.Session, &b.Number, &b.Title,
			&b.ShortTitle, &b.BillType, &b.Chamber,
			&b.SponsorID, &b.SponsorName,
			&b.CurrentStage, &b.CurrentStatus,
			&b.Category, &b.SummaryAI, &b.SummaryLoP,
			&b.FullTextURL, &b.LegisInfoURL,
			&b.IntroducedDate, &b.LastActivityDate,
		); err != nil {
			return nil, 0, fmt.Errorf("ListBills scan: %w", err)
		}
		bills = append(bills, b)
	}
	return bills, total, rows.Err()
}

// GetBill returns a single bill by ID.
func (s *Store) GetBill(id string) (BillRow, error) {
	row := s.db.QueryRow(`
		SELECT b.id, b.parliament, b.session, b.number, b.title,
		       COALESCE(b.short_title,''), COALESCE(b.bill_type,''), COALESCE(b.chamber,''),
		       COALESCE(b.sponsor_id,''), COALESCE(m.name,''),
		       COALESCE(b.current_stage,''), COALESCE(b.current_status,''),
		       COALESCE(b.category,''), COALESCE(b.summary_ai,''), COALESCE(b.summary_lop,''),
		       COALESCE(b.full_text_url,''), COALESCE(b.legisinfo_url,''),
		       COALESCE(b.introduced_date,''), COALESCE(b.last_activity_date,'')
		FROM bills b
		LEFT JOIN members m ON m.id = b.sponsor_id
		WHERE b.id = ?`, id)
	var b BillRow
	err := row.Scan(
		&b.ID, &b.Parliament, &b.Session, &b.Number, &b.Title,
		&b.ShortTitle, &b.BillType, &b.Chamber,
		&b.SponsorID, &b.SponsorName,
		&b.CurrentStage, &b.CurrentStatus,
		&b.Category, &b.SummaryAI, &b.SummaryLoP,
		&b.FullTextURL, &b.LegisInfoURL,
		&b.IntroducedDate, &b.LastActivityDate,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BillRow{}, fmt.Errorf("bill %q not found", id)
	}
	return b, err
}

// GetBillStages returns all stage records for a bill.
func (s *Store) GetBillStages(billID string) ([]BillStageRow, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(stage,''), COALESCE(chamber,''), COALESCE(date,''), COALESCE(notes,'')
		FROM bill_stages WHERE bill_id = ? ORDER BY date`, billID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BillStageRow
	for rows.Next() {
		var r BillStageRow
		if err := rows.Scan(&r.Stage, &r.Chamber, &r.Date, &r.Notes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetBillDivisions returns all divisions associated with a bill.
func (s *Store) GetBillDivisions(billID string) ([]DivisionRow, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.parliament, d.session, d.number, COALESCE(d.date,''),
		       COALESCE(d.bill_id,''), COALESCE(b.number,''),
		       COALESCE(d.description,''), d.yeas, d.nays, d.paired,
		       COALESCE(d.result,''), COALESCE(d.chamber,''), COALESCE(d.sitting_url,'')
		FROM divisions d
		LEFT JOIN bills b ON b.id = d.bill_id
		WHERE d.bill_id = ?
		ORDER BY d.date DESC`, billID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDivisionRows(rows)
}

// ── division queries ──────────────────────────────────────────────────────────

// ListDivisions returns a paginated list of divisions.
func (s *Store) ListDivisions(page, perPage int) ([]DivisionRow, int, error) {
	if perPage <= 0 {
		perPage = 50
	}
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * perPage

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM divisions").Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(`
		SELECT d.id, d.parliament, d.session, d.number, COALESCE(d.date,''),
		       COALESCE(d.bill_id,''), COALESCE(b.number,''),
		       COALESCE(d.description,''), d.yeas, d.nays, d.paired,
		       COALESCE(d.result,''), COALESCE(d.chamber,''), COALESCE(d.sitting_url,'')
		FROM divisions d
		LEFT JOIN bills b ON b.id = d.bill_id
		ORDER BY d.date DESC, d.id DESC
		LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	divs, err := scanDivisionRows(rows)
	return divs, total, err
}

func scanDivisionRows(rows *sql.Rows) ([]DivisionRow, error) {
	var out []DivisionRow
	for rows.Next() {
		var d DivisionRow
		if err := rows.Scan(
			&d.ID, &d.Parliament, &d.Session, &d.Number, &d.Date,
			&d.BillID, &d.BillNumber,
			&d.Description, &d.Yeas, &d.Nays, &d.Paired,
			&d.Result, &d.Chamber, &d.SittingURL,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ── member queries ────────────────────────────────────────────────────────────

// ListMembers returns members matching optional search/party/province filters.
func (s *Store) ListMembers(search, party, province string) ([]MemberRow, error) {
	where := []string{"1=1"}
	args := []interface{}{}

	if search != "" {
		where = append(where, "(name LIKE ? OR riding LIKE ?)")
		like := "%" + search + "%"
		args = append(args, like, like)
	}
	if party != "" {
		where = append(where, "party = ?")
		args = append(args, party)
	}
	if province != "" {
		where = append(where, "province = ?")
		args = append(args, province)
	}

	rows, err := s.db.Query(`
		SELECT id, name, COALESCE(party,''), COALESCE(riding,''), COALESCE(province,''),
		       COALESCE(role,''), COALESCE(photo_url,''), COALESCE(email,''),
		       COALESCE(website,''), COALESCE(chamber,'commons'), active
		FROM members WHERE `+strings.Join(where, " AND ")+`
		ORDER BY name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemberRows(rows)
}

// GetMember returns a single member by ID.
func (s *Store) GetMember(id string) (MemberRow, error) {
	row := s.db.QueryRow(`
		SELECT id, name, COALESCE(party,''), COALESCE(riding,''), COALESCE(province,''),
		       COALESCE(role,''), COALESCE(photo_url,''), COALESCE(email,''),
		       COALESCE(website,''), COALESCE(chamber,'commons'), active
		FROM members WHERE id = ?`, id)
	var m MemberRow
	var active int
	err := row.Scan(&m.ID, &m.Name, &m.Party, &m.Riding, &m.Province,
		&m.Role, &m.PhotoURL, &m.Email, &m.Website, &m.Chamber, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberRow{}, fmt.Errorf("member %q not found", id)
	}
	m.Active = active == 1
	return m, err
}

func scanMemberRows(rows *sql.Rows) ([]MemberRow, error) {
	var out []MemberRow
	for rows.Next() {
		var m MemberRow
		var active int
		if err := rows.Scan(&m.ID, &m.Name, &m.Party, &m.Riding, &m.Province,
			&m.Role, &m.PhotoURL, &m.Email, &m.Website, &m.Chamber, &active); err != nil {
			return nil, err
		}
		m.Active = active == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMembersByRiding searches members by riding name (partial match).
func (s *Store) GetMembersByRiding(riding string) ([]MemberRow, error) {
	rows, err := s.db.Query(`
		SELECT id, name, COALESCE(party,''), COALESCE(riding,''), COALESCE(province,''),
		       COALESCE(role,''), COALESCE(photo_url,''), COALESCE(email,''),
		       COALESCE(website,''), COALESCE(chamber,'commons'), active
		FROM members WHERE LOWER(riding) LIKE '%' || LOWER(?) || '%'
		ORDER BY name`, riding)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemberRows(rows)
}

// GetMemberVotes returns the most recent votes for a member.
func (s *Store) GetMemberVotes(id string, limit int) ([]VoteRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT mv.division_id, COALESCE(d.date,''), COALESCE(d.bill_id,''),
		       COALESCE(b.number,''), COALESCE(d.description,''),
		       mv.vote, COALESCE(d.result,'')
		FROM member_votes mv
		JOIN divisions d ON d.id = mv.division_id
		LEFT JOIN bills b ON b.id = d.bill_id
		WHERE mv.member_id = ?
		ORDER BY d.date DESC
		LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rawVote struct {
		divisionID  string
		date        string
		billID      string
		billNumber  string
		description string
		vote        string
		result      string
	}
	var rawVotes []rawVote
	for rows.Next() {
		var rv rawVote
		if err := rows.Scan(&rv.divisionID, &rv.date, &rv.billID, &rv.billNumber,
			&rv.description, &rv.vote, &rv.result); err != nil {
			return nil, err
		}
		rawVotes = append(rawVotes, rv)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if len(rawVotes) == 0 {
		return nil, nil
	}

	// Get member's party
	var party string
	_ = s.db.QueryRow("SELECT COALESCE(party,'') FROM members WHERE id = ?", id).Scan(&party)

	// Batch-fetch party majority for all divisions in one query.
	// partyMajority maps division_id → "Yea" | "Nay" | ""
	partyMajorityMap := make(map[string]string, len(rawVotes))
	if party != "" {
		divIDs := make([]string, len(rawVotes))
		for i, rv := range rawVotes {
			divIDs[i] = rv.divisionID
		}
		placeholders := strings.Repeat("?,", len(divIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, 0, len(divIDs)+1)
		for _, d := range divIDs {
			args = append(args, d)
		}
		args = append(args, party)

		pmRows, err := s.db.Query(`
			SELECT mv.division_id,
			       COALESCE(SUM(CASE WHEN mv.vote = 'Yea' THEN 1 ELSE 0 END), 0),
			       COALESCE(SUM(CASE WHEN mv.vote = 'Nay' THEN 1 ELSE 0 END), 0)
			FROM member_votes mv
			JOIN members m ON m.id = mv.member_id
			WHERE mv.division_id IN (`+placeholders+`) AND m.party = ?
			GROUP BY mv.division_id`, args...)
		if err == nil {
			defer pmRows.Close()
			for pmRows.Next() {
				var divID string
				var y, n int
				if err := pmRows.Scan(&divID, &y, &n); err == nil {
					if y > n {
						partyMajorityMap[divID] = "Yea"
					} else if n > y {
						partyMajorityMap[divID] = "Nay"
					}
				}
			}
		}
	}

	out := make([]VoteRow, 0, len(rawVotes))
	for _, rv := range rawVotes {
		partyMajority := partyMajorityMap[rv.divisionID]
		votedWithParty := partyMajority != "" && rv.vote == partyMajority
		out = append(out, VoteRow{
			DivisionID:     rv.divisionID,
			Date:           rv.date,
			BillID:         rv.billID,
			BillNumber:     rv.billNumber,
			Description:    rv.description,
			Vote:           rv.vote,
			Result:         rv.result,
			VotedWithParty: votedWithParty,
			PartyMajority:  partyMajority,
		})
	}
	return out, nil
}

// GetMemberStats computes voting statistics for a member.
func (s *Store) GetMemberStats(id string) (MemberStats, error) {
	var party string
	_ = s.db.QueryRow("SELECT COALESCE(party,'') FROM members WHERE id = ?", id).Scan(&party)

	rows, err := s.db.Query(`
		SELECT mv.division_id, mv.vote
		FROM member_votes mv
		WHERE mv.member_id = ?`, id)
	if err != nil {
		return MemberStats{}, err
	}
	defer rows.Close()

	type divVote struct {
		divisionID string
		vote       string
	}
	var votes []divVote
	for rows.Next() {
		var dv divVote
		if err := rows.Scan(&dv.divisionID, &dv.vote); err != nil {
			return MemberStats{}, err
		}
		votes = append(votes, dv)
	}
	if err := rows.Err(); err != nil {
		return MemberStats{}, err
	}
	rows.Close()

	totalVoted := len(votes)
	partyLine := 0

	// Batch-fetch party majority for all divisions in one query to avoid N+1.
	if len(votes) > 0 && party != "" {
		divIDs := make([]string, len(votes))
		memberVoteMap := make(map[string]string, len(votes))
		for i, dv := range votes {
			divIDs[i] = dv.divisionID
			memberVoteMap[dv.divisionID] = dv.vote
		}
		placeholders := strings.Repeat("?,", len(divIDs))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, 0, len(divIDs)+2)
		for _, d := range divIDs {
			args = append(args, d)
		}
		args = append(args, party, id)

		pmRows, err := s.db.Query(`
			SELECT mv.division_id,
			       COALESCE(SUM(CASE WHEN mv.vote = 'Yea' THEN 1 ELSE 0 END), 0),
			       COALESCE(SUM(CASE WHEN mv.vote = 'Nay' THEN 1 ELSE 0 END), 0)
			FROM member_votes mv
			JOIN members m ON m.id = mv.member_id
			WHERE mv.division_id IN (`+placeholders+`) AND m.party = ? AND m.id != ?
			GROUP BY mv.division_id`, args...)
		if err == nil {
			defer pmRows.Close()
			for pmRows.Next() {
				var divID string
				var y, n int
				if scanErr := pmRows.Scan(&divID, &y, &n); scanErr == nil {
					partyMajority := ""
					if y > n {
						partyMajority = "Yea"
					} else if n > y {
						partyMajority = "Nay"
					}
					if partyMajority != "" && memberVoteMap[divID] == partyMajority {
						partyLine++
					}
				}
			}
		}
	}

	const currentParliament = 45
	const currentSession = 1
	var totalDivisions int
	_ = s.db.QueryRow(`
		SELECT COUNT(*) FROM divisions
		WHERE parliament = ? AND session = ?`,
		currentParliament, currentSession).Scan(&totalDivisions)

	var voted int
	_ = s.db.QueryRow(`
		SELECT COUNT(*) FROM member_votes mv
		JOIN divisions d ON d.id = mv.division_id
		WHERE mv.member_id = ? AND d.parliament = ? AND d.session = ?`,
		id, currentParliament, currentSession).Scan(&voted)

	missed := totalDivisions - voted

	var stats MemberStats
	stats.TotalVotes = totalVoted
	if totalVoted > 0 {
		stats.PartyLinePct = (partyLine * 100) / totalVoted
		rebel := totalVoted - partyLine
		stats.RebelPct = (rebel * 100) / totalVoted
	}
	if totalDivisions > 0 {
		stats.MissedPct = (missed * 100) / totalDivisions
	}
	return stats, nil
}

// CompareMemberVotes returns the count of divisions where both MPs voted the same way,
// and the total number of divisions where both voted.
func (s *Store) CompareMemberVotes(id1, id2 string) (overlap int, total int, err error) {
	err = s.db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN mv1.vote = mv2.vote THEN 1 ELSE 0 END), 0),
			COUNT(*)
		FROM member_votes mv1
		JOIN member_votes mv2 ON mv2.division_id = mv1.division_id AND mv2.member_id = ?
		WHERE mv1.member_id = ?`, id2, id1).Scan(&overlap, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return overlap, total, err
}

// ── parliament status ─────────────────────────────────────────────────────────

// GetParliamentStatus returns the current session status based on sitting_calendar.
func (s *Store) GetParliamentStatus(parliament, session int) (ParliamentStatus, error) {
	today := time.Now().Format("2006-01-02")

	ps := ParliamentStatus{
		Parliament: parliament,
		Session:    session,
		Label:      fmt.Sprintf("%d%s Parliament, %d%s Session", parliament, ordinal(parliament), session, ordinal(session)),
	}

	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sitting_calendar
		WHERE parliament = ? AND session = ? AND date = ?`,
		parliament, session, today).Scan(&count)
	if err != nil {
		return ps, err
	}

	if count > 0 {
		ps.Status = "in_session"
		ps.Detail = "Parliament is sitting today"
	} else {
		ps.Status = "on_break"
		var nextDate string
		_ = s.db.QueryRow(`
			SELECT date FROM sitting_calendar
			WHERE parliament = ? AND session = ? AND date > ?
			ORDER BY date LIMIT 1`,
			parliament, session, today).Scan(&nextDate)
		if nextDate != "" {
			ps.Detail = "Next sitting: " + nextDate
		} else {
			ps.Detail = "No upcoming sitting dates scheduled"
		}
	}
	return ps, nil
}

// GetRecentBills returns the most recently active bills.
func (s *Store) GetRecentBills(limit int) ([]BillRow, error) {
	if limit <= 0 {
		limit = 10
	}
	f := BillFilter{Page: 1, PerPage: limit}
	bills, _, err := s.ListBills(f)
	return bills, err
}

// GetRecentDivisions returns the most recent divisions.
func (s *Store) GetRecentDivisions(limit int) ([]DivisionRow, error) {
	if limit <= 0 {
		limit = 10
	}
	divs, _, err := s.ListDivisions(1, limit)
	return divs, err
}

// ordinal returns the ordinal suffix for a number (1st, 2nd, 3rd, 4th...).
func ordinal(n int) string {
	switch n % 100 {
	case 11, 12, 13:
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

// ── phase 4: user engagement ────────────────────────────────────────────────

func userIDFromEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func scanUserRow(scanner interface{ Scan(...interface{}) error }) (UserRow, error) {
	var u UserRow
	err := scanner.Scan(
		&u.ID,
		&u.Email,
		&u.EmailVerified,
		&u.Address,
		&u.PostalCode,
		&u.FederalRidingID,
		&u.ProvincialRidingID,
		&u.CreatedAt,
		&u.EmailDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, fmt.Errorf("user not found")
	}
	return u, err
}

func (s *Store) getUserByID(id string) (UserRow, error) {
	return scanUserRow(s.db.QueryRow(`
		SELECT id, COALESCE(email,''), COALESCE(email_verified,0), COALESCE(address,''), COALESCE(postal_code,''), COALESCE(federal_riding_id,''),
		       COALESCE(provincial_riding_id,''), COALESCE(created_at,''), COALESCE(email_digest,'weekly')
		FROM users WHERE id = ?`, id))
}

func (s *Store) UpsertUser(email, postalCode string) (UserRow, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return UserRow{}, fmt.Errorf("email required")
	}
	id := userIDFromEmail(email)
	postalCode = strings.TrimSpace(postalCode)

	_, err := s.db.Exec(`
		INSERT INTO users (id, email, postal_code)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			email = excluded.email,
			postal_code = CASE WHEN excluded.postal_code != '' THEN excluded.postal_code ELSE users.postal_code END`,
		id, email, postalCode)
	if err != nil {
		return UserRow{}, err
	}

	return s.getUserByID(id)
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func codeHash(code string) string {
	return tokenHash(strings.TrimSpace(code))
}

func randomCode6() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if v < 0 {
		v = -v
	}
	return fmt.Sprintf("%06d", v%1000000), nil
}

func (s *Store) GetUserByEmail(email string) (UserRow, error) {
	id := userIDFromEmail(email)
	return s.getUserByID(id)
}

func (s *Store) UpdateUserLocation(userID, address, federalRidingID, provincialRidingID string) (UserRow, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserRow{}, fmt.Errorf("user id required")
	}

	_, err := s.db.Exec(`
		UPDATE users
		SET address = ?,
		    federal_riding_id = ?,
		    provincial_riding_id = ?
		WHERE id = ?`,
		strings.TrimSpace(address),
		strings.TrimSpace(federalRidingID),
		strings.TrimSpace(provincialRidingID),
		userID,
	)
	if err != nil {
		return UserRow{}, err
	}

	return s.getUserByID(userID)
}

func (s *Store) CreateEmailVerification(email, postalCode string, ttl time.Duration) (token string, code string, err error) {
	u, err := s.UpsertUser(email, postalCode)
	if err != nil {
		return "", "", err
	}

	// Basic anti-abuse cooldown per user to reduce rapid token churn.
	var lastCreated string
	err = s.db.QueryRow(`
		SELECT COALESCE(MAX(created_at),'')
		FROM email_verification_tokens
		WHERE user_id = ?`, u.ID).Scan(&lastCreated)
	if err != nil {
		return "", "", err
	}
	if lastCreated != "" {
		if t, parseErr := time.Parse(time.RFC3339, lastCreated); parseErr == nil {
			if time.Since(t) < time.Minute {
				return "", "", fmt.Errorf("verification recently requested")
			}
		}
	}

	token, err = randomToken(24)
	if err != nil {
		return "", "", err
	}
	code, err = randomCode6()
	if err != nil {
		return "", "", err
	}
	expires := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	tokenDigest := tokenHash(token)
	codeDigest := codeHash(code)
	_, err = s.db.Exec(`
		INSERT INTO email_verification_tokens (user_id, email, token, code, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, strings.ToLower(strings.TrimSpace(email)), tokenDigest, codeDigest, expires)
	if err != nil {
		return "", "", err
	}
	return token, code, nil
}

func (s *Store) VerifyEmailToken(token string) (UserRow, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return UserRow{}, fmt.Errorf("token required")
	}
	tokenDigest := tokenHash(token)
	tx, err := s.db.Begin()
	if err != nil {
		return UserRow{}, err
	}
	defer tx.Rollback()

	var userID, expiresAt string
	err = tx.QueryRow(`
		SELECT user_id, expires_at
		FROM email_verification_tokens
		WHERE token = ? AND used_at IS NULL`, tokenDigest).Scan(&userID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, fmt.Errorf("invalid or used token")
	}
	if err != nil {
		return UserRow{}, err
	}

	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return UserRow{}, err
	}
	if time.Now().UTC().After(exp) {
		return UserRow{}, fmt.Errorf("token expired")
	}

	if _, err := tx.Exec(`UPDATE users SET email_verified = 1 WHERE id = ?`, userID); err != nil {
		return UserRow{}, err
	}
	if _, err := tx.Exec(`UPDATE email_verification_tokens SET used_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE token = ?`, tokenDigest); err != nil {
		return UserRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return UserRow{}, err
	}

	return s.getUserByID(userID)
}

func (s *Store) VerifyEmailCode(email, code string) (UserRow, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.TrimSpace(code)
	if email == "" || code == "" {
		return UserRow{}, fmt.Errorf("email and code required")
	}
	userID := userIDFromEmail(email)
	codeDigest := codeHash(code)

	tx, err := s.db.Begin()
	if err != nil {
		return UserRow{}, err
	}
	defer tx.Rollback()

	var tokenDigest, expiresAt string
	err = tx.QueryRow(`
		SELECT token, expires_at
		FROM email_verification_tokens
		WHERE user_id = ? AND code = ? AND used_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`, userID, codeDigest).Scan(&tokenDigest, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, fmt.Errorf("invalid code")
	}
	if err != nil {
		return UserRow{}, err
	}

	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return UserRow{}, err
	}
	if time.Now().UTC().After(exp) {
		return UserRow{}, fmt.Errorf("code expired")
	}

	if _, err := tx.Exec(`UPDATE users SET email_verified = 1 WHERE id = ?`, userID); err != nil {
		return UserRow{}, err
	}
	if _, err := tx.Exec(`UPDATE email_verification_tokens SET used_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE token = ?`, tokenDigest); err != nil {
		return UserRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return UserRow{}, err
	}
	return s.GetUserByEmail(email)
}

func (s *Store) CreateSession(userID string, ttl time.Duration) (string, error) {
	sessionID, err := randomToken(24)
	if err != nil {
		return "", err
	}
	expires := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	sessionDigest := tokenHash(sessionID)
	_, err = s.db.Exec(`INSERT INTO user_sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessionDigest, userID, expires)
	return sessionID, err
}

func (s *Store) DeleteSession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM user_sessions WHERE id = ?`, tokenHash(sessionID))
	return err
}

func (s *Store) GetUserBySession(sessionID string) (UserRow, error) {
	var userID, expiresAt string
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM user_sessions WHERE id = ?`, tokenHash(sessionID)).Scan(&userID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserRow{}, fmt.Errorf("session not found")
	}
	if err != nil {
		return UserRow{}, err
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || time.Now().UTC().After(exp) {
		_ = s.DeleteSession(sessionID)
		return UserRow{}, fmt.Errorf("session expired")
	}

	return s.getUserByID(userID)
}

func (s *Store) AuthenticateOAuth(provider, providerUserID, email, postalCode string, markEmailVerified bool) (UserRow, error) {
	u, err := s.UpsertUser(email, postalCode)
	if err != nil {
		return UserRow{}, err
	}
	if markEmailVerified {
		_, err = s.db.Exec(`UPDATE users SET email_verified = 1 WHERE id = ?`, u.ID)
		if err != nil {
			return UserRow{}, err
		}
	}
	_, err = s.db.Exec(`
		INSERT INTO oauth_identities (provider, provider_user_id, user_id, email)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider, provider_user_id) DO UPDATE SET
			user_id = excluded.user_id,
			email = excluded.email`, provider, providerUserID, u.ID, strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return UserRow{}, err
	}
	return s.GetUserByEmail(email)
}

func (s *Store) FollowMember(email, postalCode, memberID string) error {
	u, err := s.UpsertUser(email, postalCode)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO user_follows (user_id, member_id)
		VALUES (?, ?)
		ON CONFLICT(user_id, member_id) DO NOTHING`, u.ID, memberID)
	return err
}

func (s *Store) ReactToBill(email, postalCode, billID, reaction, note string) error {
	reaction = strings.ToLower(strings.TrimSpace(reaction))
	if reaction != "support" && reaction != "oppose" && reaction != "neutral" {
		return fmt.Errorf("invalid reaction")
	}
	u, err := s.UpsertUser(email, postalCode)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO bill_reactions (user_id, bill_id, reaction, note)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, bill_id) DO UPDATE SET
			reaction = excluded.reaction,
			note = excluded.note,
			created_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')`,
		u.ID, billID, reaction, strings.TrimSpace(note))
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO bill_reaction_counts (bill_id, support_count, oppose_count, neutral_count, total_reactions, refreshed_at)
		SELECT
			?,
			COALESCE(SUM(CASE WHEN reaction='support' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reaction='oppose' THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN reaction='neutral' THEN 1 ELSE 0 END),0),
			COUNT(*),
			strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		FROM bill_reactions WHERE bill_id = ?
		ON CONFLICT(bill_id) DO UPDATE SET
			support_count = excluded.support_count,
			oppose_count = excluded.oppose_count,
			neutral_count = excluded.neutral_count,
			total_reactions = excluded.total_reactions,
			refreshed_at = excluded.refreshed_at`, billID, billID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Store) GetBillReactionCounts(billID string) (BillReactionCounts, error) {
	var c BillReactionCounts
	err := s.db.QueryRow(`
		SELECT bill_id, support_count, oppose_count, neutral_count, total_reactions, COALESCE(refreshed_at,'')
		FROM bill_reaction_counts WHERE bill_id = ?`, billID).Scan(
		&c.BillID, &c.SupportCount, &c.OpposeCount, &c.NeutralCount, &c.TotalReactions, &c.RefreshedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BillReactionCounts{BillID: billID}, nil
	}
	return c, err
}

func (s *Store) LogPolicySubmission(email, postalCode, memberID, subject, body, category string) error {
	u, err := s.UpsertUser(email, postalCode)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO policy_submissions (user_id, member_id, subject, body, category)
		VALUES (?, ?, ?, ?, ?)`,
		u.ID, memberID, strings.TrimSpace(subject), strings.TrimSpace(body), strings.TrimSpace(category))
	return err
}
