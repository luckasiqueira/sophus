create table webhooks
(
    id           serial  not null
        constraint webhooks_pk
            primary key,
    endpoint     uuid    not null,
    connectionId integer not null
);

