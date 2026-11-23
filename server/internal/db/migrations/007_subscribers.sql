-- Visitors who follow a status page by email (double opt-in).
CREATE TABLE subscribers (
    id           BIGSERIAL PRIMARY KEY,
    page_id      BIGINT      NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    email        TEXT        NOT NULL,
    token        TEXT        NOT NULL UNIQUE, -- confirm / unsubscribe link secret
    confirmed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX subscribers_page_email_idx ON subscribers (page_id, lower(email));
