import { FontAwesomeIcon } from "@fortawesome/react-fontawesome";
import { faXmark } from "@fortawesome/free-solid-svg-icons";

export function InboxRemoveButton({ label, disabled, onClick }: {
  label: string; disabled: boolean; onClick: () => void;
}) {
  return <button type="button" className="inbox-remove" disabled={disabled}
    aria-label={label} title={label} onClick={onClick}>
    <FontAwesomeIcon icon={faXmark} aria-hidden="true" />
  </button>;
}
