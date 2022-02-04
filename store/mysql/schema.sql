create table subscriptions
(
    id          bigint                                null,
    topic_id    bigint unsigned                       not null,
    callback    varchar(1024)                         not null,
    secret      varchar(200)                          null,
    lease       int       default 0                   null,
    created_at  timestamp default current_timestamp() not null,
    expires_at  timestamp                             null,
    constraint subscriptions_pk
        primary key (id)
);

create index subscriptions_topic_index
    on subscriptions (topic_id);

create index subscriptions_topic_callback_index
    on subscriptions (topic_id, callback);

create table topics
(
    id    bigint unsigned auto_increment primary key,
    topic varchar(512) not null
);

create index topics_topic_index on topics (topic);