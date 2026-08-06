-- D1 schema for the Tunnex licence issuance service (S12.4).
--
-- ⛔ THREE TABLES AND NO MORE. There is deliberately no `payments`, no `webhook_events`, no `orders` — a
-- schema built "ready for payments" is dormant machinery, and this repo has already reduced that class
-- once by removal (S8.4 round-3 HALT). The payment seam is ONE PARAGRAPH in
-- docs/S12.4-issuance-decisions.md §4 and nothing else. If you are adding a table here for a provider that
-- does not exist yet, you have left the seam and started the machinery.

-- requests — what a company asked for, and what a human decided about it.
--
-- ⚠ A REQUEST IS NEVER DELETED. A refused request is a refusal we may need to explain, and a granted one
-- is the record of WHY a key exists. Under offline verification a key cannot be recalled, so the request
-- row is the only account of why it was minted at all.
CREATE TABLE IF NOT EXISTS requests (
    id           TEXT PRIMARY KEY,          -- uuid
    company      TEXT NOT NULL,
    contact_name TEXT NOT NULL DEFAULT '',
    email        TEXT NOT NULL,
    domain       TEXT NOT NULL,             -- derived from email at submit; the trial key AND the licence subject
    band         TEXT NOT NULL CHECK (band IN ('trial', 'starter', 'growth', 'scale')),
    term_months  INTEGER NOT NULL CHECK (term_months > 0),
    use_case     TEXT NOT NULL DEFAULT '',
    -- pending: waiting for a human. issued: a key was minted. refused: a human said no.
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'issued', 'refused')),
    -- ⚠ The advisory trial verdict computed at SUBMIT time, carried to the reviewer with its reason. It is
    -- advice to a person, never a wall — a legitimate second trial is a conversation, and refusing it at
    -- the form means the person most likely to become a customer meets a locked screen instead of a human.
    trial_note   TEXT NOT NULL DEFAULT '',
    refuse_note  TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,          -- epoch seconds
    decided_at   INTEGER
);
CREATE INDEX IF NOT EXISTS requests_pending_idx ON requests (created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS requests_domain_idx ON requests (domain);

-- issued_keys — every key that has ever left this service.
--
-- ⛔ THE SIGNED KEY IS STORED. Not the private key: the ISSUED ARTEFACT. Under offline verification we
-- cannot ask a deployment what it is running, so this table is the ONLY record of what we put into the
-- world. Losing it means being unable to answer "what does this customer actually have", forever.
--
-- ⚠ AND IT IS THE ONLY THING THAT MAKES A SUSPECTED COMPROMISE INVESTIGABLE AT ALL — not detectable
-- (§3.2 of the paper: detection is impossible by construction), but at least it lets a key someone shows
-- us be checked against the set we actually minted.
CREATE TABLE IF NOT EXISTS issued_keys (
    id          TEXT PRIMARY KEY,           -- the licence id embedded in the payload
    request_id  TEXT NOT NULL REFERENCES requests (id),
    domain      TEXT NOT NULL,
    band        TEXT NOT NULL,
    kid         TEXT NOT NULL,              -- ⭐ D4: which signing key minted it. A set is unusable without this
    issued_at   INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    licence_key TEXT NOT NULL,              -- the full wire string, exactly as emailed
    emailed_at  INTEGER,                    -- null => minted but delivery not confirmed; the admin view says so
    CHECK (expires_at > issued_at)
);
CREATE INDEX IF NOT EXISTS issued_keys_domain_idx ON issued_keys (domain);
CREATE INDEX IF NOT EXISTS issued_keys_kid_idx ON issued_keys (kid);

-- trial_domains — one trial per company domain.
--
-- ⛔ THIS STOPS ACCIDENTS, NOT ADVERSARIES. It keys on the email domain; a subdomain, a look-alike or a
-- second corporate domain defeats it completely. The real control at today's volume is that a human reads
-- every request — TRUE ONLY WHILE THE VOLUME IS SMALL.
--
-- ⚠ RE-READ docs/S12.4-issuance-decisions.md D2 WHEN REQUEST VOLUME OUTGROWS INDIVIDUAL REVIEW. At that
-- point this control is unimplemented and this table is still here looking like it works. The recorded
-- upgrade is DNS-TXT domain-ownership proof, reusing the S2.5 domain-capture verifier.
CREATE TABLE IF NOT EXISTS trial_domains (
    domain          TEXT PRIMARY KEY,
    trial_issued_at INTEGER NOT NULL,
    request_id      TEXT NOT NULL REFERENCES requests (id)
);
