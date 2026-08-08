-- Vendors are named after the API the credential authenticates rather than
-- after a CLI that presents it. The enum kept its numbers, so nothing on the
-- wire changed -- but these rows hold the names, and the CHECK constraint
-- names them too.
--
-- Dropped first: the constraint rejects the new values, so an UPDATE ahead of
-- it fails on its own rows. Only subscriptions carries one; the denormalized
-- column on attachments never had one and does not gain one here.
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_vendor_check;

UPDATE subscriptions SET vendor = 'anthropic' WHERE vendor = 'claude';
UPDATE subscriptions SET vendor = 'openai' WHERE vendor = 'codex';
UPDATE subscription_attachments SET vendor = 'anthropic' WHERE vendor = 'claude';
UPDATE subscription_attachments SET vendor = 'openai' WHERE vendor = 'codex';

ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_vendor_check CHECK (vendor IN ('anthropic', 'openai'));