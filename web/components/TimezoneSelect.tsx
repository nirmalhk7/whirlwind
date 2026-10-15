"use client";

import Select from "react-select";
import { rawTimeZones, timeZonesNames } from "@vvo/tzdb";

type TimezoneOption = { value: string; label: string; search: string };
const normalize = (value: string) => value.toLowerCase().replace(/[_/]/g, " ");
const metadata = new Map(rawTimeZones.flatMap(zone => zone.group.map(name => [name, zone] as const)));
const options: TimezoneOption[] = [...new Set(["UTC", ...timeZonesNames])].map(value => {
  const zone = metadata.get(value);
  return {
    value,
    label: zone ? `${value} · ${zone.alternativeName}` : value,
    search: normalize([value, zone?.alternativeName, zone?.countryName, zone?.mainCities.join(" "), zone?.abbreviation].join(" ")),
  };
});

export function TimezoneSelect({ value, disabled, onChange }: { value: string; disabled: boolean; onChange: (value: string) => void }) {
  const selected = options.find(option => option.value === value) ?? { value, label: value, search: normalize(value) };
  return <Select<TimezoneOption>
    inputId="ideation-timezone"
    instanceId="ideation-timezone"
    name="ideation_timezone"
    className="timezone-select"
    classNamePrefix="timezone-select"
    unstyled
    options={options.some(option => option.value === value) ? options : [selected, ...options]}
    value={selected}
    onChange={option => { if (option) onChange(option.value); }}
    filterOption={({ data }, query) => data.search.includes(normalize(query).trim())}
    isDisabled={disabled}
    isSearchable
    required
    placeholder="Search timezones…"
    noOptionsMessage={() => "No matching timezones"}
    aria-describedby="ideation-timezone-help"
  />;
}
