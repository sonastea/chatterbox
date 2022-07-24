CREATE SCHEMA chatterbox
    AUTHORIZATION postgres;


CREATE TABLE chatterbox."User" (
	id int NOT NULL GENERATED ALWAYS AS IDENTITY,
	"name" varchar NOT NULL,
	email varchar NOT NULL,
	"password" varchar NOT NULL,
	CONSTRAINT user_pk PRIMARY KEY (id)
);


CREATE TABLE chatterbox."Room" (
	id int NOT NULL GENERATED ALWAYS AS IDENTITY,
	private boolean NOT NULL DEFAULT false,
	"name" varchar NOT NULL,
	description varchar NULL,
	created_at date NULL default now(),
	owner_id int NOT NULL,
	CONSTRAINT "Room_pk" PRIMARY KEY (id),
	CONSTRAINT "Room_Owner_FK" FOREIGN KEY (owner_id) REFERENCES chatterbox."User"(id)
);


CREATE TABLE chatterbox."Message" (
	id int NOT NULL GENERATED ALWAYS AS IDENTITY,
	body varchar NULL,
	room_id int NOT NULL,
	author_id int NOT NULL,
    "timestamp" timetz NOT NULL default now(),
	CONSTRAINT "Message_pk" PRIMARY KEY (id),
	CONSTRAINT "Message_Author_FK" FOREIGN KEY (author_id) REFERENCES chatterbox."User"(id),
	CONSTRAINT "Message_Room_FK" FOREIGN KEY (room_id) REFERENCES chatterbox."Room"(id)
);
