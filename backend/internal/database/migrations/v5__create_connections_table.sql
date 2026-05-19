create table connections
(
    id        serial                              not null
        constraint connections_pk
            primary key,
    name      varchar                             not null,
    number    varchar                             not null,
    status    varchar                             not null,
    companyId integer                             not null,
    qrcode    text                                not null,
    createdAt timestamp default current_timestamp not null
);

