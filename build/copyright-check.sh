#!/bin/bash
# Copyright (c) 2020 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project


#Project start year
origin_year=2016
#Back up year if system time is null or incorrect
back_up_year=2019
#Currrent year
current_year=$(date +"%Y")

TRAVIS_BRANCH=$1

if [[ "x${TRAVIS_BRANCH}" != "x" ]]; then
  FILES_TO_SCAN=$(git diff --name-only --diff-filter=AM ${TRAVIS_BRANCH}...HEAD | grep -v -f <(sed 's/\([.|]\)/\\\1/g; s/\?/./g ; s/\*/.*/g' .copyrightignore))
else
  FILES_TO_SCAN=$(find . -type f | grep -Ev '(\.git)' | grep -v -f <(sed 's/\([.|]\)/\\\1/g; s/\?/./g ; s/\*/.*/g' .copyrightignore))
fi

if [ -z "$current_year" ] || [ $current_year -lt $origin_year ]; then
  echo "Can't get correct system time\n  >>Use back_up_year=$back_up_year as current_year to check copyright in the file $f\n"
  current_year=$back_up_year
fi

lic_redhat_identifier=" Copyright (c) ${current_year} Red Hat, Inc."
lic_redhat_identifier_2020=" Copyright (c) 2020 Red Hat, Inc."

#Used to signal an exit
ERROR=0
RETURNCODE=0

echo "##### Copyright check #####"
#Loop through all files. Ignore .FILENAME types
for f in $FILES_TO_SCAN; do
  if [ ! -f "$f" ]; then
   continue
  fi

  # Flags that indicate the licenses to check for
  must_have_redhat_license=false

  FILETYPE=$(basename ${f##*.})
  case "${FILETYPE}" in
  	js | go | scss | properties | java | rb | sh )
  		COMMENT_PREFIX=""
  		;;
  	*)
      #printf " Extension $FILETYPE not considered !!!\n"
      continue
  esac

  #Read the first 15 lines, most Copyright headers use the first 10 lines.
  header=`head -15 $f`

  # Strip directory prefix, if any
  if [[ $f == "./"* ]]; then
    f=${f:2}
  fi

  printf " ========>>>>>>   Scanning $f . . .\n"
  must_have_redhat_license=true

  if [[ "${must_have_redhat_license}" == "true" ]] && [[ "$header" != *"${lic_redhat_identifier}"* ]] && [[ "$header" != *"${lic_redhat_identifier_2020}"* ]]; then
    printf " Missing copyright\n >> Could not find [${lic_redhat_identifier}] in the file.\n"
    ERROR=1
  fi

  #Add a status message of OK, if all copyright lines are found
  if [[ "$ERROR" == 0 ]]; then
    printf "OK\n"
  else
    RETURNCODE=$ERROR
    ERROR=0 # Reset error
  fi
done

echo "##### Copyright check ##### ReturnCode: ${RETURNCODE}"
exit $RETURNCODE
