CREATE TABLE renter_metrics (
	renter_key BLOB NOT NULL CHECK(length(renter_key) = 32), -- 32 bytes
	date_created INTEGER NOT NULL, -- UNIX timestamp

	active_contracts INTEGER NOT NULL DEFAULT 0 CHECK (active_contracts >= 0),
	renewed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (renewed_contracts >= 0),
	successful_contracts INTEGER NOT NULL DEFAULT 0 CHECK (successful_contracts >= 0),
	failed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (failed_contracts >= 0),

	active_size INTEGER NOT NULL DEFAULT 0 CHECK (active_size >= 0), -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0 CHECK (total_size >= 0), -- total size of data in historical contracts

	locked_allowance BLOB NOT NULL CHECK(length(locked_allowance) = 16), -- currency
	spent_allowance BLOB NOT NULL CHECK(length(spent_allowance) = 16), -- currency

	PRIMARY KEY (renter_key, date_created)
);


CREATE TABLE host_metrics (
	host_key BLOB NOT NULL CHECK(length(host_key) = 32), -- 32 bytes
	date_created INTEGER NOT NULL, -- UNIX timestamp

	active_contracts INTEGER NOT NULL DEFAULT 0 CHECK (active_contracts >= 0),
	renewed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (renewed_contracts >= 0),
	successful_contracts INTEGER NOT NULL DEFAULT 0 CHECK (successful_contracts >= 0),
	failed_contracts INTEGER NOT NULL DEFAULT 0 CHECK (failed_contracts >= 0),

	active_size INTEGER NOT NULL DEFAULT 0 CHECK (active_size >= 0), -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0 CHECK (total_size >= 0), -- total size of data in historical contracts

	burnt_collateral BLOB NOT NULL CHECK(length(burnt_collateral) = 16), -- currency
	locked_collateral BLOB NOT NULL CHECK(length(locked_collateral) = 16), -- currency
	risked_collateral BLOB NOT NULL CHECK(length(risked_collateral) = 16), -- currency
	potential_revenue BLOB NOT NULL CHECK(length(potential_revenue) = 16), -- currency
	earned_revenue BLOB NOT NULL CHECK(length(earned_revenue) = 16), -- currency

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

	active_size INTEGER NOT NULL DEFAULT 0, -- total size of data in active contracts
	total_size INTEGER NOT NULL DEFAULT 0, -- total size of data in historical contracts

	potential_revenue BLOB NOT NULL CHECK(length(potential_revenue) = 16), -- currency
	earned_revenue BLOB NOT NULL CHECK(length(earned_revenue) = 16), -- currency
	locked_collateral BLOB NOT NULL CHECK(length(locked_collateral) = 16), -- currency
	risked_collateral BLOB NOT NULL CHECK(length(risked_collateral) = 16), -- currency
	burnt_collateral BLOB NOT NULL CHECK(length(burnt_collateral) = 16), -- currency

	locked_allowance BLOB NOT NULL CHECK(length(locked_allowance) = 16), -- currency
	spent_allowance BLOB NOT NULL CHECK(length(spent_allowance) = 16) -- currency
);

CREATE TABLE global_settings (
	id INTEGER PRIMARY KEY NOT NULL DEFAULT 0 CHECK (id = 0), -- enforce a single row
	db_version INTEGER NOT NULL, -- used for migrations
	last_index BLOB CHECK(length(last_index) = 40) -- 32 + 8
);
