-- PostGIS-dependent fixtures (GeoJSON cases 1616-1618, geotest area). Requires
-- the postgis extension to be available in this database. See
-- fixtures/README.md for the PostGIS requirement and which cases depend on it.
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE TABLE test.shops (
    id        int primary key,
    address   text,
    shop_geom geometry(POINT, 4326)
);
INSERT INTO test.shops (id, address, shop_geom) VALUES
  (1, '1369 Cambridge St',     'SRID=4326;POINT(-71.10044 42.373695)'),
  (2, '757 Massachusetts Ave', 'SRID=4326;POINT(-71.10543 42.366432)'),
  (3, '605 W Kendall St',      'SRID=4326;POINT(-71.081924 42.36437)');
GRANT SELECT ON TABLE test.shops TO postgrest_test_anonymous;

-- Isolated schema for geo+json feature tests (mutations/RPC/embeds).
-- Deliberately NOT in the shared conformance instance's db_schemas, so
-- the frozen suite (incl. the openapi document cases) never sees it.
CREATE SCHEMA geotest;
CREATE TABLE geotest.shops (
    id        int primary key,
    address   text,
    shop_geom geometry(POINT, 4326)
);
INSERT INTO geotest.shops SELECT * FROM test.shops;
CREATE TABLE geotest.shop_bles (
    id      int primary key,
    name    text,
    coords  geometry(POINT, 4326),
    shop_id int REFERENCES geotest.shops(id)
);
INSERT INTO geotest.shop_bles (id, name, coords, shop_id) VALUES
  (1, 'battery',    'SRID=4326;POINT(-71.10044 42.373695)', 1),
  (2, 'car-key',    'SRID=4326;POINT(-71.10543 42.366432)', 1),
  (3, 'headphones', 'SRID=4326;POINT(-71.081924 42.36437)', 2);
CREATE TABLE geotest.plain (id int primary key, label text);
INSERT INTO geotest.plain VALUES (1, 'no-geometry');
CREATE FUNCTION geotest.get_shops() RETURNS SETOF geotest.shops
  LANGUAGE sql STABLE AS 'SELECT * FROM geotest.shops ORDER BY id';
CREATE FUNCTION geotest.get_shop_geom(id int) RETURNS geometry
  LANGUAGE sql STABLE
  AS 'SELECT shop_geom FROM geotest.shops WHERE shops.id = get_shop_geom.id';
GRANT USAGE ON SCHEMA geotest TO postgrest_test_anonymous;
GRANT ALL ON ALL TABLES IN SCHEMA geotest TO postgrest_test_anonymous;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA geotest TO postgrest_test_anonymous;
