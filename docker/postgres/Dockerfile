FROM postgres:18-alpine

# Swedish hunspell dictionary for the ts_swedish text search config (see internal/migrations/schema.sql)
ARG DICT_URL=https://raw.githubusercontent.com/LibreOffice/dictionaries/44f958e5773b8701b64af9aaba0c728d2418542b/sv_SE/dictionaries
ADD ${DICT_URL}/sv_SE.aff /usr/local/share/postgresql/tsearch_data/sv_se.affix
ADD ${DICT_URL}/sv_SE.dic /usr/local/share/postgresql/tsearch_data/sv_se.dict
RUN chmod 644 /usr/local/share/postgresql/tsearch_data/sv_se.*
