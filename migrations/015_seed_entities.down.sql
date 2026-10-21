-- Migration 015 (down): remove the seeded landmarks.
--
-- Only the eight review landmarks are removed, not every place: a workspace
-- that grew its own places keeps them.

DELETE FROM entities
 WHERE (name, type) IN (
        ('Tanah Lot', 'destination'),
        ('Monas', 'monument'),
        ('Benteng Rotterdam', 'historic'),
        ('Tanjung Puting', 'park'),
        ('Danau Toba', 'recreation'),
        ('Gembira Loka', 'zoo'),
        ('Sate Padang Ajo Ramon', 'culinary'),
        ('Hotel Novotel Balikpapan', 'lodging')
       );
