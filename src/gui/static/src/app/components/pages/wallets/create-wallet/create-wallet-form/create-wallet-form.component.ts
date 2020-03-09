import { switchMap } from 'rxjs/operators';
import { Component, OnInit, OnDestroy, Input, Output, EventEmitter } from '@angular/core';
import { FormControl, FormGroup, Validators } from '@angular/forms';
import { SubscriptionLike, Subject, of } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';

import { ApiService } from '../../../../../services/api.service';
import { SeedWordDialogComponent, WordAskedReasons } from '../../../../layout/seed-word-dialog/seed-word-dialog.component';
import { MsgBarService } from '../../../../../services/msg-bar.service';
import { ConfirmationParams, ConfirmationComponent, DefaultConfirmationButtons } from '../../../../layout/confirmation/confirmation.component';
import { OperationError } from '../../../../../utils/operation-error';
import { processServiceError } from '../../../../../utils/errors';

/**
 * Data entered in an instance of CreateWalletFormComponent.
 */
export class WalletFormData {
  /**
   * If the form is for creating a new wallet (true) or loading a walled using a seed (false).
   */
  creatingNewWallet: boolean;
  /**
   * Label for the wallet.
   */
  label: string;
  /**
   * Seed for the wallet.
   */
  seed: string;
  /**
   * If set, the wallet must be encrypted with this password.
   */
  password: string;
  /**
   * If true, the seed was entered using the assisted mode.
   */
  enterSeedWithAssistance: boolean;
  /**
   * If creating a new wallet, the last automatically generated seed for the assisted mode. If
   * loading a wallet, the last valid seed the user entered using the assisted procedure.
   */
  lastAssistedSeed: string;
  /**
   * Last seed the user entered using the manual mode.
   */
  lastCustomSeed: string;
  /**
   * If creating a new wallet, how many words the automatically generated seed for the
   * assisted mode has. If loading a wallet, how many words the seed entered by the user has.
   */
  numberOfWords: number;
  /**
   * If the user entered a standard seed, if the manual mode was being used.
   */
  customSeedIsNormal: boolean;
}

/**
 * Form for creating or loading a software wallet.
 */
@Component({
  selector: 'app-create-wallet-form',
  templateUrl: './create-wallet-form.component.html',
  styleUrls: ['./create-wallet-form.component.scss'],
})
export class CreateWalletFormComponent implements OnInit, OnDestroy {
  // If the form is for creating a new wallet (true) or loading a walled using a seed (false).
  @Input() create: boolean;
  // If the form is being shown on the wizard (true) or not (false).
  @Input() onboarding: boolean;
  // Allows to deactivate the form while the system is busy.
  @Input() busy = false;
  // Emits when the user asks for the wallet ot be created.
  @Output() createRequested = new EventEmitter<void>();

  form: FormGroup;
  // If true, the user must enter the ssed using the asisted mode.
  enterSeedWithAssistance = true;
  // If the user confirmed the seed using the asisted mode, while creating a new wallet.
  assistedSeedConfirmed = false;
  // If the user entered a standard seed using the manual mode.
  customSeedIsNormal = true;
  // If the user entered a non-standard seed using the manual mode and confirmed to use it.
  customSeedAccepted = false;
  // If the user selected that the wallet must be created encrypted.
  encrypt = true;
  // If creating a new wallet, the last automatically generated seed for the assisted mode. If
  // loading a wallet, the last valid seed the user entered using the assisted procedure.
  lastAssistedSeed = '';
  // How many words the last autogenerated seed for the assisted mode has, when creating
  // a new wallet.
  numberOfAutogeneratedWords = 0;
  // If the system is currently checking the custom seed entered by the user.
  checkingCustomSeed = false;

  // Emits every time the seed should be checked again, to know if it is a standard seed.
  private seed: Subject<string> = new Subject<string>();

  private statusSubscription: SubscriptionLike;
  private seedValiditySubscription: SubscriptionLike;

  // Saves the words the user enters while using the assisted mode.
  private partialSeed: string[];

  constructor(
    private apiService: ApiService,
    private dialog: MatDialog,
    private msgBarService: MsgBarService,
  ) { }

  ngOnInit() {
    if (!this.onboarding) {
      this.initForm();
    } else {
      this.initForm(false, null);
    }
  }

  ngOnDestroy() {
    this.msgBarService.hide();
    this.statusSubscription.unsubscribe();
    this.seedValiditySubscription.unsubscribe();
  }

  // Allows to know if the form is valid.
  get isValid(): boolean {
    // When entering the seed manually, the system must have finished checking the seed and the
    // seed must be normal or the user must confirm the usage of a custom seed. When using the
    // assisted mode, the user must enter the seed in the appropriate way.
    return this.form.valid && !this.checkingCustomSeed &&
      (
        (!this.enterSeedWithAssistance && (this.customSeedIsNormal || this.customSeedAccepted)) ||
        (this.create && this.enterSeedWithAssistance && this.assistedSeedConfirmed) ||
        (!this.create && this.enterSeedWithAssistance && this.lastAssistedSeed.length > 2)
      );
  }

  // Sets if the user has acepted to use a manually entered non-standard seed.
  onCustomSeedAcceptance(event) {
    this.customSeedAccepted = event.checked;
  }

  // Sets the user selection regarding whether the wallet must be encrypted or not.
  setEncrypt(event) {
    this.encrypt = event.checked;
    this.form.updateValueAndValidity();
  }

  // Returns the data entered on the form.
  getData(): WalletFormData {
    return {
      creatingNewWallet: this.create,
      label: this.form.value.label,
      seed: this.enterSeedWithAssistance ? this.lastAssistedSeed : this.form.value.seed,
      password: !this.onboarding && this.encrypt ? this.form.value.password : null,
      enterSeedWithAssistance: this.enterSeedWithAssistance,
      lastAssistedSeed: this.lastAssistedSeed,
      lastCustomSeed: this.form.value.seed,
      numberOfWords: !this.create ? this.form.value.number_of_words : this.numberOfAutogeneratedWords,
      customSeedIsNormal: this.customSeedIsNormal,
    };
  }

  // Switches between the assisted mode and the manual mode for entering the seed.
  changeSeedType() {
    this.msgBarService.hide();

    if (!this.enterSeedWithAssistance) {
      this.enterSeedWithAssistance = true;
      this.removeConfirmations();
    } else {
      // Ask for confirmation before making the change.
      const confirmationParams: ConfirmationParams = {
        text: this.create ? 'wallet.new.seed.custom-seed-warning-text' : 'wallet.new.seed.custom-seed-warning-text-recovering',
        headerText: 'common.warning-title',
        checkboxText: this.create ? 'common.generic-confirmation-check' : null,
        defaultButtons: DefaultConfirmationButtons.ContinueCancel,
        redTitle: true,
      };

      ConfirmationComponent.openDialog(this.dialog, confirmationParams).afterClosed().subscribe(confirmationResult => {
        if (confirmationResult) {
          this.enterSeedWithAssistance = false;
          this.removeConfirmations();
        }
      });
    }
  }

  // Starts the assisted procedure for entering the seed, if the user is trying to load
  // an existing wallet.
  enterSeed() {
    if (!this.create) {
      this.partialSeed = [];
      this.askForWord(0);
      this.msgBarService.hide();
    }
  }

  // Starts the assisted procedure for confirming the automatically generated seed, if the
  // user is trying to create a new wallet.
  confirmSeed() {
    if (!this.assistedSeedConfirmed) {
      this.partialSeed = [];
      this.askForWord(0);
      this.msgBarService.hide();
    }
  }

  /**
   * Recursively asks the user to enter the words of the seed.
   * @param wordIndex Index of the word which is going to be requested on this step. Must be
   * 0 when starting to ask for the words.
   */
  private askForWord(wordIndex: number) {
    // Open the modal window for entering the seed word.
    return SeedWordDialogComponent.openDialog(this.dialog, {
      reason: this.create ? WordAskedReasons.CreatingSoftwareWallet : WordAskedReasons.RecoveringSoftwareWallet,
      wordNumber: wordIndex + 1,
    }).afterClosed().subscribe(word => {
      if (word) {
        // If creating a new wallet, check if the user entered the requested word.
        if (this.create) {
          const lastSeedWords = this.lastAssistedSeed.split(' ');
          if (word !== lastSeedWords[wordIndex]) {
            this.msgBarService.showError('wallet.new.seed.incorrect-word-error');

            return;
          }
        }

        // Add the entered word to the list of words the user already entered.
        this.partialSeed[wordIndex] = word;
        wordIndex += 1;

        if ((this.create && wordIndex < this.numberOfAutogeneratedWords) || (!this.create && wordIndex < this.form.controls['number_of_words'].value)) {
          this.askForWord(wordIndex);
        } else {
          if (this.create) {
            // Set the seed as confirmed.
            this.assistedSeedConfirmed = true;
          } else {
            // Build the seed.
            let enteredSeed = '';
            this.partialSeed.forEach(currentWord => enteredSeed += currentWord + ' ');
            enteredSeed = enteredSeed.substr(0, enteredSeed.length - 1);

            // Check the seed and use it only if it is valid.
            this.apiService.post('wallet/seed/verify', {seed: enteredSeed}, {useV2: true})
              .subscribe(() => this.lastAssistedSeed = enteredSeed, () => this.msgBarService.showError('wallet.new.seed.invalid-seed-error'));
          }
        }
      }
    });
  }

  /**
   * Inits or resets the form.
   * @param create If the form is for creating a new wallet (true) or loading a walled using
   * a seed (false). Use null to avoid changing the value set using the html tag.
   * @param data Data to populate the form.
   */
  initForm(create: boolean = null, data: WalletFormData = null) {
    this.msgBarService.hide();

    create = create !== null ? create : this.create;

    this.lastAssistedSeed = '';
    this.enterSeedWithAssistance = true;

    const validators = [];
    if (create) {
      validators.push(this.seedMatchValidator.bind(this));
    }
    if (!this.onboarding) {
      // The password is entered on a different form while using the wizard.
      validators.push(this.validatePasswords.bind(this));
    }
    validators.push(this.mustHaveSeed.bind(this));

    this.form = new FormGroup({}, validators);
    this.form.addControl('label', new FormControl(data ? data.label : '', [Validators.required]));
    this.form.addControl('seed', new FormControl(data ? data.lastCustomSeed : ''));
    this.form.addControl('confirm_seed', new FormControl(data ? data.lastCustomSeed : ''));
    this.form.addControl('password', new FormControl());
    this.form.addControl('confirm_password', new FormControl());
    this.form.addControl('number_of_words', new FormControl(!this.create && data && data.numberOfWords ? data.numberOfWords : 12));

    this.removeConfirmations(false);

    // Create a new random seed.
    if (create && !data) {
      this.generateSeed(128);
    }

    // Use the provided data.
    if (data) {
      this.enterSeedWithAssistance = data.enterSeedWithAssistance;
      this.lastAssistedSeed = data.lastAssistedSeed;
      this.assistedSeedConfirmed = true;
      this.customSeedAccepted = true;
      this.customSeedIsNormal = data.customSeedIsNormal;

      if (this.create) {
        this.numberOfAutogeneratedWords = data.numberOfWords;
      }
    }

    if (this.statusSubscription && !this.statusSubscription.closed) {
      this.statusSubscription.unsubscribe();
    }
    this.statusSubscription = this.form.statusChanges.subscribe(() => {
      // Invaidate the custom seed confirmation if the data on the form is changed.
      this.customSeedAccepted = false;
      this.seed.next(this.form.get('seed').value);
    });

    this.subscribeToSeedValidation();
  }

  // Generates a new random seed for when creating a new wallet.
  generateSeed(entropy: number) {
    if (entropy === 128) {
      this.numberOfAutogeneratedWords = 12;
    } else {
      this.numberOfAutogeneratedWords = 24;
    }

    this.apiService.get('wallet/newSeed', { entropy }).subscribe(response => {
      this.lastAssistedSeed = response.seed;
      this.form.get('seed').setValue(response.seed);
      this.removeConfirmations();
    });
  }

  // Request the wallet to be created or loaded.
  requestCreation() {
    this.createRequested.emit();
  }

  /**
   * Removes the confirmations the user could have made for accepting the seed.
   * @param cleanSecondSeedField If true, the second field for manually entering a seed (the
   * one used for confirming the seed by entering it again) will be cleaned.
   */
  private removeConfirmations(cleanSecondSeedField = true) {
    this.customSeedAccepted = false;
    this.assistedSeedConfirmed = false;
    if (cleanSecondSeedField) {
      this.form.get('confirm_seed').setValue('');
    }
    this.form.updateValueAndValidity();
  }

  // Makes the component continually check if the user has manually entered a non-standard seed.
  private subscribeToSeedValidation() {
    if (this.seedValiditySubscription) {
      this.seedValiditySubscription.unsubscribe();
    }

    this.seedValiditySubscription = this.seed.asObservable().pipe(switchMap(seed => {
      // Verify the seed if it was entered manually and was confirmed.
      if ((!this.seedMatchValidator() || !this.create) && !this.enterSeedWithAssistance) {
        this.checkingCustomSeed = true;

        return this.apiService.post('wallet/seed/verify', {seed}, {useV2: true});
      } else {
        return of(0);
      }
    })).subscribe(() => {
      // The entered seed does not have problems.
      this.customSeedIsNormal = true;
      this.checkingCustomSeed = false;
    }, (error: OperationError) => {
      this.checkingCustomSeed = false;
      // If the node said the seed is not standard, ask the user for confirmation before
      // allowing to use it.
      error = processServiceError(error);
      if (error && error.originalError && error.originalError.status === 422) {
        this.customSeedIsNormal = false;
      } else {
        this.customSeedIsNormal = true;
        this.msgBarService.showWarning('wallet.new.seed-checking-error');
      }
      this.subscribeToSeedValidation();
    });
  }

  // Validator that, if the wallet must be encrypted, checks if the 2 password match.
  private validatePasswords() {
    if (this.encrypt) {
      if (this.form && this.form.get('password') && this.form.get('confirm_password')) {
        if (this.form.get('password').value) {
          if (this.form.get('password').value !== this.form.get('confirm_password').value) {
            return { NotEqual: true };
          }
        } else {
          return { Required: true };
        }
      } else {
        return { Required: true };
      }
    }

    return null;
  }

  // Validator that checks if a seed has been manually entered, if the assisted mode is
  // not activated.
  private mustHaveSeed() {
    if (!this.enterSeedWithAssistance) {
      if ((this.form.get('seed').value as string) === '') {
        return { Required: true };
      }
    }

    return null;
  }

  // Validator that checks if the manually entered seeds match, if the assisted mode is
  // not activated.
  private seedMatchValidator() {
    if (this.enterSeedWithAssistance) {
      return null;
    }

    if (this.form && this.form.get('seed') && this.form.get('confirm_seed')) {
      return this.form.get('seed').value === this.form.get('confirm_seed').value ? null : { NotEqual: true };
    } else {
      return { NotEqual: true };
    }
  }
}
