create table tokens
(
    id     SERIAL
        constraint tokens_pk
            primary key,
    apiToken  VARCHAR(24) NOT NULL,
    connectionKey VARCHAR(36) NOT NULL

);
