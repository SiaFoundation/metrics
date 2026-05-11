CREATE TABLE renter_metrics (
	renter_key BLOB NOT NULL CHECK(length(renter_key) = 32), -- 32 bytes
	date_created INTEGER NOT NULL, -- UNIX timestamp

	active_contracts INTEGER NOT NULL DEFAULT 0 CHECK (active_contracts >= 0),
	renewed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (renewed_contracts >= 0),
	successful_contracts INTEGER NOT NULL DEFAULT 0 CHECK (successful_contracts >= 0),
	failed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (failed_contracts >= 0),
	revision_count INTEGER NOT NULL DEFAULT 0 CHECK (revision_count >= 0), -- cumulative v2 contract revisions for this renter

	active_size INTEGER NOT NULL DEFAULT 0 CHECK (active_size >= 0), -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0 CHECK (total_size >= 0), -- total size of data in historical contracts; tracks chain state so can decrease on shrink revisions
	bytes_uploaded INTEGER NOT NULL DEFAULT 0 CHECK (bytes_uploaded >= 0), -- monotonically-increasing total bytes ever added to contracts

	locked_allowance BLOB NOT NULL CHECK(length(locked_allowance) = 16), -- currency
	spent_allowance BLOB NOT NULL CHECK(length(spent_allowance) = 16), -- currency
	tax BLOB NOT NULL DEFAULT x'00000000000000000000000000000000' CHECK(length(tax) = 16), -- currency: cumulative siafund tax paid

	first_seen INTEGER NOT NULL DEFAULT 0, -- UNIX ms: first block in which this renter had any v2 contract event
	last_active INTEGER NOT NULL DEFAULT 0, -- UNIX ms: most recent block in which this renter had any v2 contract event

	PRIMARY KEY (renter_key, date_created)
);


CREATE TABLE host_metrics (
	host_key BLOB NOT NULL CHECK(length(host_key) = 32), -- 32 bytes
	date_created INTEGER NOT NULL, -- UNIX timestamp

	active_contracts INTEGER NOT NULL DEFAULT 0 CHECK (active_contracts >= 0),
	renewed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (renewed_contracts >= 0),
	successful_contracts INTEGER NOT NULL DEFAULT 0 CHECK (successful_contracts >= 0),
	failed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (failed_contracts >= 0),
	revision_count INTEGER NOT NULL DEFAULT 0 CHECK (revision_count >= 0), -- cumulative v2 contract revisions for this host

	active_size INTEGER NOT NULL DEFAULT 0 CHECK (active_size >= 0), -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0 CHECK (total_size >= 0), -- total size of data in historical contracts; tracks chain state so can decrease on shrink revisions
	bytes_uploaded INTEGER NOT NULL DEFAULT 0 CHECK (bytes_uploaded >= 0), -- monotonically-increasing total bytes ever added to contracts

	burnt_collateral BLOB NOT NULL CHECK(length(burnt_collateral) = 16), -- currency
	locked_collateral BLOB NOT NULL CHECK(length(locked_collateral) = 16), -- currency
	risked_collateral BLOB NOT NULL CHECK(length(risked_collateral) = 16), -- currency
	potential_revenue BLOB NOT NULL CHECK(length(potential_revenue) = 16), -- currency
	earned_revenue BLOB NOT NULL CHECK(length(earned_revenue) = 16), -- currency

	first_seen INTEGER NOT NULL DEFAULT 0, -- UNIX ms: first block in which this host had any v2 contract event
	last_active INTEGER NOT NULL DEFAULT 0, -- UNIX ms: most recent block in which this host had any v2 contract event

	PRIMARY KEY (host_key, date_created)
);

CREATE TABLE metrics (
	date_created INTEGER PRIMARY KEY, -- UNIX timestamp

	hosts INTEGER NOT NULL DEFAULT 0,
	renters INTEGER NOT NULL DEFAULT 0,

	active_contracts INTEGER NOT NULL DEFAULT 0,
	renewed_contracts INTEGER NOT NULL DEFAULT 0,
	successful_contracts INTEGER NOT NULL DEFAULT 0,
	failed_contracts INTEGER NOT NULL DEFAULT 0,
	v2_transaction_count INTEGER NOT NULL DEFAULT 0, -- cumulative v2 transactions in all indexed blocks
	revision_count INTEGER NOT NULL DEFAULT 0, -- cumulative v2 contract revisions in all indexed blocks

	active_size INTEGER NOT NULL DEFAULT 0, -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0, -- total size of data in historical contracts; tracks chain state so can decrease on shrink revisions
	bytes_uploaded INTEGER NOT NULL DEFAULT 0, -- monotonically-increasing total bytes ever added to network contracts
	active_byte_days INTEGER NOT NULL DEFAULT 0, -- cumulative byte-days of storage (Riemann sum: active_size * block_interval / 1 day per block)

	potential_revenue BLOB NOT NULL CHECK(length(potential_revenue) = 16), -- currency
	earned_revenue BLOB NOT NULL CHECK(length(earned_revenue) = 16), -- currency
	locked_collateral BLOB NOT NULL CHECK(length(locked_collateral) = 16), -- currency
	risked_collateral BLOB NOT NULL CHECK(length(risked_collateral) = 16), -- currency
	burnt_collateral BLOB NOT NULL CHECK(length(burnt_collateral) = 16), -- currency

	locked_allowance BLOB NOT NULL CHECK(length(locked_allowance) = 16), -- currency
	spent_allowance BLOB NOT NULL CHECK(length(spent_allowance) = 16), -- currency
	tax BLOB NOT NULL DEFAULT x'00000000000000000000000000000000' CHECK(length(tax) = 16) -- currency: cumulative siafund tax paid
);

CREATE TABLE global_settings (
	id INTEGER PRIMARY KEY NOT NULL DEFAULT 0 CHECK (id = 0), -- enforce a single row
	db_version INTEGER NOT NULL, -- used for migrations
	last_index BLOB CHECK(length(last_index) = 40) -- 32 + 8
);
