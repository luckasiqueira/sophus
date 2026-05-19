create table logs_errors
(
    id        serial
        constraint logs_errors_pk
            primary key,
    level     varchar,
    error     varchar,
    details   varchar,
    details_2 timestamp
);

