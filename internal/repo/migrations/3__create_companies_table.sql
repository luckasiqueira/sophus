-- +goose Up
create table companies
(
    id        serial                              not null
        constraint companies_pk
            primary key,
    name      varchar                             not null,
    email     varchar                             not null,
    isActive  boolean                             not null,
    planId    integer                             not null,
    createdAt timestamp default current_timestamp not null,
    dueDate   timestamp                           not null,
    timezone  varchar
);
