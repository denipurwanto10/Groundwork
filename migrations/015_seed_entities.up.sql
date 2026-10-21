-- Migration 015 (up): 8 landmarks across Indonesia, one per type.
--
-- The review data used to live in an unnumbered seed.sql, so it never ran
-- with the migration chain and the "No entities yet." state could not be
-- reproduced without a second database. It is a numbered migration now, so a
-- fresh database comes up with something to look at.
--
-- Want the empty state back? Run scripts/empty-state.sql: it removes the
-- landmarks (and the demo fleet) and leaves the "No entities yet." screen.
--
-- Re-runnable: a landmark that is already there is not added twice.

INSERT INTO entities (name, type, status, latitude, longitude)
SELECT v.name, v.type, v.status, v.latitude, v.longitude
  FROM (VALUES
        ('Tanah Lot', 'destination', 'active', -8.6210::double precision, 115.0870::double precision),
        ('Monas', 'monument', 'active', -6.1754, 106.8272),
        ('Benteng Rotterdam', 'historic', 'active', -5.1340, 119.4050),
        ('Tanjung Puting', 'park', 'active', -3.0000, 111.9500),
        ('Danau Toba', 'recreation', 'active', 2.6840, 98.8750),
        ('Gembira Loka', 'zoo', 'active', -7.8030, 110.3970),
        ('Sate Padang Ajo Ramon', 'culinary', 'active', -0.9470, 100.3560),
        ('Hotel Novotel Balikpapan', 'lodging', 'active', -1.2650, 116.8320)
       ) AS v(name, type, status, latitude, longitude)
 WHERE NOT EXISTS (
        SELECT 1 FROM entities e
         WHERE e.name = v.name AND e.type = v.type
       );
