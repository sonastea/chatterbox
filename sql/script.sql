CREATE SCHEMA IF NOT EXISTS chatterbox AUTHORIZATION postgres;


SET search_path TO chatterbox;


CREATE TABLE IF NOT EXISTS "User" (
	id SERIAL PRIMARY KEY,
    xid varchar NOT NULL,
	"name" varchar NOT NULL,
	email varchar NOT NULL,
	"password" varchar NOT NULL,
	CONSTRAINT "User_Email_un" UNIQUE (email),
	CONSTRAINT "User_xid_un" UNIQUE (xid)
);


CREATE TABLE IF NOT EXISTS "Room" (
	id SERIAL PRIMARY KEY,
    xid varchar NOT NULL,
	private boolean NOT NULL DEFAULT false,
	"name" varchar NOT NULL,
	description varchar NULL,
	created_at date NULL DEFAULT now(),
	owner_id varchar NOT NULL,
	CONSTRAINT "Room_Name_un" UNIQUE (name),
	CONSTRAINT "Room_xid_un" UNIQUE (xid),
	CONSTRAINT "Room_Owner_FK" FOREIGN KEY (owner_id) REFERENCES chatterbox."User"(xid)
);


CREATE TABLE IF NOT EXISTS "Message" (
	id SERIAL PRIMARY KEY,
	body varchar NULL,
	room_id varchar NOT NULL,
	author_id varchar NOT NULL,
    "timestamp" timetz NOT NULL DEFAULT now(),
	CONSTRAINT "Message_Author_FK" FOREIGN KEY (author_id) REFERENCES chatterbox."User"(xid),
	CONSTRAINT "Message_Room_FK" FOREIGN KEY (room_id) REFERENCES chatterbox."Room"(xid)
);
